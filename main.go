package main

import (
	"embed"
	"log"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/github"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed assets/dockiva_icon2.png
var trayIcon []byte

func main() {
	appService, err := NewAppService()
	if err != nil {
		log.Fatal(err)
	}

	dockerService, err := NewDockerService()
	if err != nil {
		log.Fatal(err)
	}

	settings := appService.Snapshot()

	if settings.DockerEndpoint != "" {
		resolvedEndpoint := resolveExternalDockerEndpoint(settings.DockerEndpoint)
		dockerService.SetConfiguredDockerEndpoint(
			resolvedEndpoint,
		)
		if resolvedEndpoint != settings.DockerEndpoint {
			settings.DockerEndpoint = resolvedEndpoint
			if err := appService.UpdateSettings(settings); err != nil {
				log.Printf("could not persist repaired Docker endpoint: %v", err)
			}
		}
	} else {
		settings.DockerEndpoint =
			dockerService.ConfiguredDockerEndpoint()

		if settings.DockerEndpoint != "" {
			if err := appService.UpdateSettings(
				settings,
			); err != nil {
				log.Printf(
					"could not persist Docker endpoint: %v",
					err,
				)
			}
		}
	}

	var provider string
	if settings.EngineBackend == "vmfree" {
		// Restore identity without blocking window creation; startup runs below.
		dockerService.replaceRuntime(newVMFreeRuntime())
		provider = "vmfree"
	} else {
		provider, err = dockerService.SelectEngineBackend(settings.EngineBackend, settings.WSLDistro, settings.DockerEndpoint)
	}

	if err != nil {
		log.Printf("could not restore engine backend %q: %v", settings.EngineBackend, err)
	} else {
		settingsChanged := false
		if settings.RuntimeProvider != provider {
			settings.RuntimeProvider = provider
			settingsChanged = true
		}
		if normalizeEngineBackend(settings.EngineBackend) == "external" {
			resolvedEndpoint := dockerService.ConfiguredDockerEndpoint()
			if resolvedEndpoint != "" && settings.DockerEndpoint != resolvedEndpoint {
				settings.DockerEndpoint = resolvedEndpoint
				settingsChanged = true
			}
		}
		if settingsChanged {
			if err := appService.UpdateSettings(settings); err != nil {
				log.Printf("could not persist restored engine settings: %v", err)
			}
		}
	}

	if socket, ok := argValue("--core"); ok {
		runCore(socket, dockerService, appService, settings)
		return
	}

	startHidden := settings.StartHidden || hasArg("--hidden")

	app := application.New(application.Options{
		Name:        "Dockiva",
		Description: "Fast cross-platform container development environment",
		Services: []application.Service{
			application.NewService(dockerService),
			application.NewService(appService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	githubUpdates, err := github.New(github.Config{
		Repository:    "Tomnerb/Dockiva",
		ChecksumAsset: "SHA256SUMS",
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := app.Updater.Init(updater.Config{
		CurrentVersion: currentVersion(),
		Providers: []updater.Provider{
			requiredChecksumProvider{provider: githubUpdates},
		},
		Window: updater.WindowNone,
	}); err != nil {
		log.Fatal(err)
	}

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:           "main",
		Title:          "Dockiva",
		Width:          1320,
		Height:         820,
		MinWidth:       980,
		MinHeight:      650,
		URL:            "/",
		EnableFileDrop: true,
		Hidden:         startHidden,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 56,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInsetUnified,
		},
	})

	window.OnWindowEvent(events.Common.WindowFilesDropped, func(event *application.WindowEvent) {
		files := event.Context().DroppedFiles()
		if len(files) > 0 {
			app.Event.Emit("dockiva:files-dropped", files)
		}
	})

	window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if appService.Snapshot().CloseToTray {
			window.Hide()
			event.Cancel()
		}
	})

	trayController := NewTrayController(
		app,
		window,
		dockerService,
		appService,
		trayIcon,
	)
	trayController.Start()

	go dockerService.AutoRecoverEngine(
		settings.EngineBackend,
		settings.WSLDistro,
		settings.AutoReconnectEngine,
		settings.StartEngineOnLaunch,
	)

	if err := app.Run(); err != nil {
		trayController.Close()
		log.Fatal(err)
	}

	trayController.Close()
}

func hasArg(target string) bool {
	for _, arg := range os.Args[1:] {
		if arg == target {
			return true
		}
	}
	return false
}
