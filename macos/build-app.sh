#!/bin/bash
# Builds macos/build/Dockiva.app: the SwiftUI front end, the Go core beside it
# (Contents/MacOS/dockiva-core) and the engine resources the core finds at
# ../Resources (vmfree-engine, dockiva-vmm, guest), copied from a Wails build.
#
#   macos/build-app.sh [RESOURCES_DIR]   (default: bin/dockiva.app/Contents/Resources)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
resources=${1:-$root/bin/dockiva.app/Contents/Resources}
version=$(tr -d '[:space:]' < "$root/VERSION")
app=$root/macos/build/Dockiva.app

swift build -c release --package-path "$root/macos"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp -f "$(swift build -c release --package-path "$root/macos" --show-bin-path)/Dockiva" "$app/Contents/MacOS/Dockiva"
(cd "$root" && go build -trimpath -ldflags "-s -w" -o "$app/Contents/MacOS/dockiva-core" .)
cp -f "$root/build/darwin/icons.icns" "$app/Contents/Resources/icons.icns"
for item in vmfree-engine dockiva-vmm guest; do
    if [ -e "$resources/$item" ]; then
        cp -R "$resources/$item" "$app/Contents/Resources/"
    else
        echo "note: $resources/$item not found; the VM-free engine will be unavailable" >&2
    fi
done

cat > "$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundlePackageType</key><string>APPL</string>
    <key>CFBundleName</key><string>Dockiva</string>
    <key>CFBundleDisplayName</key><string>Dockiva</string>
    <key>CFBundleExecutable</key><string>Dockiva</string>
    <key>CFBundleIdentifier</key><string>com.tomnerb.dockiva.native</string>
    <key>CFBundleVersion</key><string>$version</string>
    <key>CFBundleShortVersionString</key><string>$version</string>
    <key>CFBundleIconFile</key><string>icons</string>
    <key>LSMinimumSystemVersion</key><string>14.0</string>
    <key>LSApplicationCategoryType</key><string>public.app-category.developer-tools</string>
    <key>NSHighResolutionCapable</key><true/>
    <key>NSHumanReadableCopyright</key><string>© 2026 Dockiva</string>
</dict>
</plist>
EOF

# Ad hoc signatures for the two executables; the engine resources keep their own
# (with the hypervisor entitlement), so the bundle is not signed --deep.
codesign --force --sign - "$app/Contents/MacOS/dockiva-core"
codesign --force --sign - "$app"
echo "$app"
