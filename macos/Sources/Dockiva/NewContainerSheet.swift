import SwiftUI

/// The request DockerService.CreateContainer takes (RuntimeCreateContainerRequest).
struct CreateContainerRequest: Encodable {
    struct Port: Encodable { var hostPort: Int; var containerPort: Int; var `protocol` = "tcp"; var hostIp = "127.0.0.1" }
    var name: String
    var image: String
    var command: [String]
    var autoStart: Bool
    var portMappings: [Port]
    var env: [String]
}

/// Runs an image as a new container, like `docker run -d`: image, name, published
/// ports, environment and an optional command. Templates fill in common services.
struct NewContainerSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss

    struct PortRow: Identifiable { let id = UUID(); var host = ""; var container = "" }
    struct EnvRow: Identifiable { let id = UUID(); var key = ""; var value = "" }

    @State private var image = ""
    @State private var name = ""
    @State private var command = ""
    @State private var ports: [PortRow] = []
    @State private var env: [EnvRow] = []
    @State private var start = true
    @State private var creating = false
    @State private var error: String?

    private let accent = SidebarItem.containers.accent

    struct Template: Identifiable {
        var id: String { title }
        let title: String, image: String, name: String
        var ports: [(Int, Int)] = []
        var env: [(String, String)] = []
        var command = ""
    }

    private let templates: [Template] = [
        Template(title: "PostgreSQL", image: "postgres:16-alpine", name: "postgres", ports: [(5432, 5432)], env: [("POSTGRES_PASSWORD", "postgres")]),
        Template(title: "Redis", image: "redis:7-alpine", name: "redis", ports: [(6379, 6379)]),
        Template(title: "MySQL", image: "mysql:8", name: "mysql", ports: [(3306, 3306)], env: [("MYSQL_ROOT_PASSWORD", "mysql")]),
        Template(title: "MongoDB", image: "mongo:7", name: "mongo", ports: [(27017, 27017)]),
        Template(title: "Nginx", image: "nginx:alpine", name: "nginx", ports: [(8080, 80)]),
        Template(title: "Node.js", image: "node:22-alpine", name: "node", command: "sleep infinity"),
    ]

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 12) {
                ImageIcon(image: image.isEmpty ? "box" : image, running: true, size: 36)
                VStack(alignment: .leading, spacing: 1) {
                    Text("New container").font(.title2.bold())
                    Text("Runs an image, like docker run -d. Ports publish on 127.0.0.1.").font(.caption).foregroundStyle(.secondary)
                }
            }

            ScrollView(.horizontal) {
                HStack(spacing: 8) {
                    ForEach(templates) { t in
                        Button { apply(t) } label: {
                            HStack(spacing: 6) {
                                ImageIcon(image: t.image, running: true, size: 18)
                                Text(t.title)
                            }
                        }
                        .buttonStyle(SecondaryButtonStyle())
                    }
                }
            }
            .scrollIndicators(.hidden)

            Form {
                TextField("Image", text: $image, prompt: Text("e.g. postgres:16-alpine"))
                TextField("Name", text: $name, prompt: Text("e.g. postgres"))
                TextField("Command", text: $command, prompt: Text("Optional, e.g. sleep infinity"))

                Section {
                    ForEach($ports) { $row in
                        HStack {
                            TextField("Host", text: $row.host, prompt: Text("Host port"))
                            Image(systemName: "arrow.right").foregroundStyle(.secondary)
                            TextField("Container", text: $row.container, prompt: Text("Container port"))
                            removeButton { ports.removeAll { $0.id == row.id } }
                        }
                        .labelsHidden()
                    }
                    Button { ports.append(PortRow()) } label: { Label("Add port", systemImage: "plus") }.buttonStyle(.borderless)
                } header: { Text("Ports (TCP, on 127.0.0.1)") }

                Section {
                    ForEach($env) { $row in
                        HStack {
                            TextField("Key", text: $row.key, prompt: Text("KEY"))
                            Text("=").foregroundStyle(.secondary)
                            TextField("Value", text: $row.value, prompt: Text("value"))
                            removeButton { env.removeAll { $0.id == row.id } }
                        }
                        .labelsHidden()
                    }
                    Button { env.append(EnvRow()) } label: { Label("Add variable", systemImage: "plus") }.buttonStyle(.borderless)
                } header: { Text("Environment") }

                Toggle("Start the container after creating it", isOn: $start)
            }
            .formStyle(.grouped)
            .frame(minHeight: 320)

            if let error {
                Label(error, systemImage: "exclamationmark.triangle.fill").font(.callout).foregroundStyle(.red).textSelection(.enabled)
            }

            HStack {
                if creating {
                    ProgressView().controlSize(.small)
                    Text("Pulling the image if needed…").font(.caption).foregroundStyle(.secondary)
                }
                Spacer()
                Button("Cancel") { dismiss() }.buttonStyle(SecondaryButtonStyle()).keyboardShortcut(.cancelAction)
                Button(action: create) { Label("Create", systemImage: "plus") }
                    .buttonStyle(PrimaryButtonStyle(color: accent))
                    .keyboardShortcut(.defaultAction)
                    .disabled(creating || image.trimmingCharacters(in: .whitespaces).isEmpty || name.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(22)
        .frame(width: 560)
        .onChange(of: image) { old, new in
            // Suggest a name from the image while the name still follows it.
            if name.isEmpty || name == ImageStyle.name(old) { name = ImageStyle.name(new) }
        }
    }

    private func removeButton(_ action: @escaping () -> Void) -> some View {
        Button(action: action) { Image(systemName: "minus.circle.fill").foregroundStyle(.secondary) }.buttonStyle(.plain)
    }

    private func apply(_ t: Template) {
        image = t.image
        name = t.name
        command = t.command
        ports = t.ports.map { PortRow(host: String($0.0), container: String($0.1)) }
        env = t.env.map { EnvRow(key: $0.0, value: $0.1) }
        error = nil
    }

    /// The request, or a message saying which field is wrong.
    private func request() -> Result<CreateContainerRequest, Error> {
        struct Invalid: LocalizedError { let errorDescription: String? }
        var mappings: [CreateContainerRequest.Port] = []
        for row in ports where !(row.host.isEmpty && row.container.isEmpty) {
            guard let host = Int(row.host.trimmingCharacters(in: .whitespaces)), (1...65535).contains(host),
                  let container = Int(row.container.trimmingCharacters(in: .whitespaces)), (1...65535).contains(container)
            else { return .failure(Invalid(errorDescription: "Ports must be numbers from 1 to 65535: \(row.host) → \(row.container)")) }
            mappings.append(.init(hostPort: host, containerPort: container))
        }
        var variables: [String] = []
        for row in env where !row.key.isEmpty {
            guard !row.key.contains("=") && !row.key.contains(" ") else {
                return .failure(Invalid(errorDescription: "Environment names cannot contain = or spaces: \(row.key)"))
            }
            variables.append("\(row.key)=\(row.value)")
        }
        // ponytail: splits on spaces; quoted arguments need a JSON-array field if users ask.
        let args = command.split(separator: " ", omittingEmptySubsequences: true).map(String.init)
        return .success(CreateContainerRequest(name: name.trimmingCharacters(in: .whitespaces), image: image.trimmingCharacters(in: .whitespaces),
                                               command: args, autoStart: start, portMappings: mappings, env: variables))
    }

    private func create() {
        switch request() {
        case .failure(let failure): error = failure.localizedDescription
        case .success(let request):
            creating = true
            error = nil
            Task {
                defer { creating = false }
                if let message = await model.createContainer(request) {
                    error = message
                } else {
                    dismiss()
                }
            }
        }
    }
}
