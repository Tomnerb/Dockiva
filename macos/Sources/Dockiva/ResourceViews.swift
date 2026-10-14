import SwiftUI

struct ImagesView: View {
    @Environment(AppModel.self) private var model
    @State private var selection: ImageInfo.ID?
    @State private var reference = ""
    @State private var pulling = false
    @State private var output: String?

    var body: some View {
        Table(model.images, selection: $selection) {
            TableColumn("Image") { Text($0.title) }
            TableColumn("ID", value: \.shortId).width(110)
            TableColumn("Size") { Text($0.size.byteString).monospacedDigit() }.width(90)
            TableColumn("Created") { Text(Date(timeIntervalSince1970: TimeInterval($0.created)), style: .relative) }.width(120)
            TableColumn("In use") { Text($0.containers > 0 ? "\($0.containers)" : "—") }.width(60)
        }
        .contextMenu(forSelectionType: ImageInfo.ID.self) { ids in
            Button("Remove", role: .destructive) { remove(ids) }
            Button("Force remove", role: .destructive) { remove(ids, force: true) }
        }
        .safeAreaInset(edge: .top) {
            HStack {
                TextField("Pull an image, e.g. postgres:16-alpine", text: $reference)
                    .textFieldStyle(.roundedBorder)
                    .onSubmit(pull)
                Button(pulling ? "Pulling…" : "Pull", action: pull).disabled(pulling || reference.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            .padding(10)
            .background(.bar)
        }
        .sheet(item: Binding(get: { output.map(OutputSheet.init) }, set: { if $0 == nil { output = nil } })) { $0 }
        .toolbar {
            Button(role: .destructive) { if let selection { remove([selection]) } } label: { Label("Remove", systemImage: "trash") }
                .disabled(selection == nil)
        }
        .navigationTitle("Images")
        .navigationSubtitle(model.images.reduce(0) { $0 + $1.size }.byteString)
    }

    private func pull() {
        let ref = reference.trimmingCharacters(in: .whitespaces)
        guard !ref.isEmpty else { return }
        pulling = true
        Task {
            if let result = await model.cliAction("DockerService.PullImage", [ref]) { output = result; reference = "" }
            pulling = false
        }
    }

    private func remove(_ ids: Set<ImageInfo.ID>, force: Bool = false) {
        Task { for id in ids { await model.perform("DockerService.RemoveImage", [id, force]) } }
    }
}

struct VolumesView: View {
    @Environment(AppModel.self) private var model
    @State private var selection: VolumeInfo.ID?
    @State private var newName = ""

    var body: some View {
        Table(model.volumes, selection: $selection) {
            TableColumn("Name", value: \.name)
            TableColumn("Driver", value: \.driver).width(80)
            TableColumn("Mountpoint", value: \.mountpoint)
        }
        .contextMenu(forSelectionType: VolumeInfo.ID.self) { ids in
            Button("Show in Finder") {
                for id in ids {
                    if let v = model.volumes.first(where: { $0.id == id }) {
                        NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: v.mountpoint)
                    }
                }
            }
            Button("Remove", role: .destructive) { Task { for id in ids { await model.perform("DockerService.RemoveVolume", [id, false]) } } }
        }
        .safeAreaInset(edge: .top) {
            HStack {
                TextField("New volume name", text: $newName).textFieldStyle(.roundedBorder)
                Button("Create") {
                    let name = newName
                    Task { if await model.cliAction("DockerService.CreateVolume", [name, "local"]) != nil { newName = "" } }
                }
                .disabled(newName.isEmpty)
            }
            .padding(10)
            .background(.bar)
        }
        .navigationTitle("Volumes")
    }
}

struct NetworksView: View {
    @Environment(AppModel.self) private var model
    @State private var newName = ""

    var body: some View {
        Table(model.networks) {
            TableColumn("Name", value: \.name)
            TableColumn("ID", value: \.shortId).width(110)
            TableColumn("Driver", value: \.driver).width(80)
            TableColumn("Scope", value: \.scope).width(70)
        }
        .contextMenu(forSelectionType: NetworkInfo.ID.self) { ids in
            Button("Remove", role: .destructive) { Task { for id in ids { await model.perform("DockerService.RemoveNetwork", [id]) } } }
        }
        .safeAreaInset(edge: .top) {
            HStack {
                TextField("New network name", text: $newName).textFieldStyle(.roundedBorder)
                Button("Create") {
                    let name = newName
                    Task { if await model.cliAction("DockerService.CreateNetwork", [name, "bridge"]) != nil { newName = "" } }
                }
                .disabled(newName.isEmpty)
            }
            .padding(10)
            .background(.bar)
        }
        .navigationTitle("Networks")
    }
}

struct EngineView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        Form {
            Section("Engine") {
                LabeledContent("Backend", value: model.engine?.backend ?? model.settings?.engineBackend ?? "—")
                LabeledContent("State") {
                    Text(model.engine?.running == true ? "Running" : "Stopped")
                        .foregroundStyle(model.engine?.running == true ? .green : .secondary)
                }
                if let endpoint = model.engine?.endpoint { LabeledContent("Endpoint", value: endpoint).textSelection(.enabled) }
                if let message = model.engine?.message, !message.isEmpty { Text(message).font(.callout).foregroundStyle(.secondary) }
                HStack {
                    if model.engine?.running == true {
                        Button("Stop engine") { Task { await model.engineAction(start: false) } }
                    } else {
                        Button("Start engine") { Task { await model.engineAction(start: true) } }.buttonStyle(.borderedProminent)
                    }
                    if model.engineBusy { ProgressView().controlSize(.small) }
                }
                .disabled(model.engineBusy || model.engine?.supported == false)
            }
            Section("Docker") {
                LabeledContent("Connected", value: model.status?.connected == true ? "Yes" : "No")
                if let api = model.status?.apiVersion, !api.isEmpty { LabeledContent("API version", value: api) }
                if let error = model.status?.error, !error.isEmpty { Text(error).font(.callout).foregroundStyle(.red) }
                if let endpoint = model.engine?.endpoint {
                    LabeledContent("Use from a terminal") {
                        Text("export DOCKER_HOST=\(endpoint)").font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                    }
                }
            }
        }
        .formStyle(.grouped)
        .navigationTitle("Engine")
        .task { await model.refreshEngine() }
    }
}

struct SettingsView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        Form {
            if let settings = model.settings {
                Toggle("Start the engine when Dockiva opens", isOn: binding(settings, \.startEngineOnLaunch))
                Toggle("Reconnect to the engine automatically", isOn: binding(settings, \.autoReconnectEngine))
                LabeledContent("Engine backend", value: settings.engineBackend)
            } else {
                ProgressView()
            }
        }
        .formStyle(.grouped)
        .padding()
    }

    private func binding(_ settings: AppSettings, _ key: WritableKeyPath<AppSettings, Bool>) -> Binding<Bool> {
        Binding(get: { model.settings?[keyPath: key] ?? settings[keyPath: key] }, set: { value in
            var next = model.settings ?? settings
            next[keyPath: key] = value
            Task { await model.updateSettings(next) }
        })
    }
}

struct OutputSheet: View, Identifiable {
    let output: String
    @Environment(\.dismiss) private var dismiss
    var id: String { output }

    var body: some View {
        VStack(alignment: .trailing) {
            ScrollView {
                Text(stripANSI(output)).font(.system(.caption, design: .monospaced)).textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            Button("Done") { dismiss() }.keyboardShortcut(.defaultAction)
        }
        .padding()
        .frame(width: 560, height: 360)
    }
}
