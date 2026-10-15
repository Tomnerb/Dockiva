package main

// Dockiva's core API: the services the Wails window binds, served over a unix
// socket for native front ends (the SwiftUI app). One JSON object per line, each way:
//
//	request  {"id":1,"method":"DockerService.ListContainers","args":[]}
//	response {"id":1,"result":[...]}  or  {"id":1,"error":"..."}
//	event    {"event":"dockiva:docker-changed","data":{...}}
//
// The socket is created 0600 in a private directory: only the user's own
// processes reach it, as with the Docker socket.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
)

type coreEvent struct {
	Name string `json:"event"`
	Data any    `json:"data"`
}

var coreSubscribers = struct {
	sync.Mutex
	set map[chan coreEvent]struct{}
}{set: map[chan coreEvent]struct{}{}}

// emitEvent sends an event to the Wails window, when one runs, and to every core
// API client. A slow client misses events rather than holding the sender.
func emitEvent(name string, data any) {
	if app := application.Get(); app != nil {
		app.Event.Emit(name, data)
	}
	coreSubscribers.Lock()
	defer coreSubscribers.Unlock()
	for ch := range coreSubscribers.set {
		select {
		case ch <- coreEvent{name, data}:
		default:
		}
	}
}

// Lifecycle methods the core runs itself; clients never call them.
var coreHiddenMethods = map[string]bool{"ServiceStartup": true, "ServiceShutdown": true, "AutoRecoverEngine": true}

type coreRequest struct {
	ID     uint64            `json:"id"`
	Method string            `json:"method"`
	Args   []json.RawMessage `json:"args"`
}

type coreResponse struct {
	ID     uint64 `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// coreCall runs "Service.Method" with JSON arguments, as the Wails bindings do.
func coreCall(services map[string]any, method string, args []json.RawMessage) (result any, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%s: %v", method, v)
		}
	}()
	var service, name string
	for i := range method {
		if method[i] == '.' {
			service, name = method[:i], method[i+1:]
			break
		}
	}
	target, ok := services[service]
	if !ok || coreHiddenMethods[name] {
		return nil, fmt.Errorf("unknown method %q", method)
	}
	m := reflect.ValueOf(target).MethodByName(name)
	if !m.IsValid() {
		return nil, fmt.Errorf("unknown method %q", method)
	}
	t := m.Type()
	if t.IsVariadic() || t.NumIn() != len(args) {
		return nil, fmt.Errorf("%s takes %d arguments, got %d", method, t.NumIn(), len(args))
	}
	in := make([]reflect.Value, len(args))
	for i, raw := range args {
		v := reflect.New(t.In(i))
		if err := json.Unmarshal(raw, v.Interface()); err != nil {
			return nil, fmt.Errorf("%s argument %d: %w", method, i+1, err)
		}
		in[i] = v.Elem()
	}
	out := m.Call(in)
	if n := len(out); n > 0 && t.Out(n-1) == errorType {
		if e, _ := out[n-1].Interface().(error); e != nil {
			return nil, e
		}
		out = out[:n-1]
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out[0].Interface(), nil
}

// serveCore accepts core API clients on a unix socket until ctx ends.
func serveCore(ctx context.Context, socket string, services map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	if st, err := os.Lstat(socket); err == nil {
		if st.Mode()&fs.ModeSocket == 0 {
			return fmt.Errorf("%s exists and is not a socket", socket)
		}
		if err := os.Remove(socket); err != nil { // a previous core's
			return err
		}
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		listener.Close()
		return err
	}
	go func() {
		<-ctx.Done()
		listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go serveCoreClient(conn, services)
	}
}

func serveCoreClient(conn net.Conn, services map[string]any) {
	defer conn.Close()
	var writeMu sync.Mutex
	encoder := json.NewEncoder(conn)
	write := func(v any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		if err := encoder.Encode(v); err != nil {
			conn.Close()
		}
	}

	events := make(chan coreEvent, 256)
	done := make(chan struct{})
	coreSubscribers.Lock()
	coreSubscribers.set[events] = struct{}{}
	coreSubscribers.Unlock()
	defer func() {
		coreSubscribers.Lock()
		delete(coreSubscribers.set, events)
		coreSubscribers.Unlock()
		close(done)
	}()
	go func() {
		for {
			select {
			case ev := <-events:
				write(ev)
			case <-done:
				return
			}
		}
	}()

	decoder := json.NewDecoder(conn)
	for {
		var req coreRequest
		if err := decoder.Decode(&req); err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.Printf("core API client: %v", err)
			}
			return
		}
		go func() { // calls such as PullImage run long; others proceed meanwhile
			result, err := coreCall(services, req.Method, req.Args)
			resp := coreResponse{ID: req.ID, Result: result}
			if err != nil {
				resp.Error = err.Error()
			}
			write(resp)
		}()
	}
}

// runCore is `dockiva --core SOCKET`: the services without a window, for a native
// front end. It ends when its standard input closes (the front end exited).
func runCore(socket string, dockerService *DockerService, appService *AppService, settings AppSettings) {
	ctx, cancel := context.WithCancel(context.Background())
	if err := dockerService.ServiceStartup(ctx, application.ServiceOptions{}); err != nil {
		log.Fatal(err)
	}
	go dockerService.AutoRecoverEngine(settings.EngineBackend, settings.WSLDistro, settings.AutoReconnectEngine, settings.StartEngineOnLaunch)
	if updates, err := updaterConfig(); err != nil {
		log.Printf("core updater: %v", err)
	} else {
		coreUpdater = updater.New(coreUpdateHost{quit: cancel})
		if err := coreUpdater.Init(updates); err != nil {
			log.Printf("core updater: %v", err)
		}
	}
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		cancel()
	}()
	services := map[string]any{"DockerService": dockerService, "AppService": appService}
	if err := serveCore(ctx, socket, services); err != nil {
		log.Fatal(err)
	}
	if err := dockerService.ServiceShutdown(); err != nil {
		log.Printf("core shutdown: %v", err)
	}
	_ = os.Remove(socket)
}

// argValue is the argument after name on the command line (dockiva --core SOCKET).
// coreUpdateHost is the updater's host in the core: updater events go to the
// front end, and Quit (after the swap helper starts) tells the front end to quit
// before the core exits, so the helper can replace Dockiva.app.
type coreUpdateHost struct{ quit func() }

func (coreUpdateHost) Emit(name string, data ...any) bool {
	var payload any = data
	if len(data) == 1 {
		payload = data[0]
	}
	emitEvent(name, payload)
	return true
}
func (coreUpdateHost) OnEvent(string, func(any)) func()                      { return func() {} }
func (coreUpdateHost) OpenWindow(updater.WindowOptions) updater.WindowHandle { return nil } // Window: WindowNone
func (h coreUpdateHost) Quit() {
	emitEvent("dockiva:quit-for-update", nil)
	time.Sleep(300 * time.Millisecond) // the event reaches the front end before the connection closes
	h.quit()
}

func argValue(name string) (string, bool) { return argAfter(os.Args, name) }

func argAfter(args []string, name string) (string, bool) {
	for i := 1; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1], true
		}
	}
	return "", false
}
