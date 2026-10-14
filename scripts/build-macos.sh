#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

ARCH="arm64"
GUEST_ASSETS=""
INSTALL=false
EXTERNAL_ONLY=false
RELEASE=false
UNSIGNED_RELEASE=false
SIGN_IDENTITY="${DOCKIVA_SIGN_IDENTITY:-}"
NOTARY_PROFILE="${DOCKIVA_NOTARY_PROFILE:-}"
RELEASE_TEMP=""
APP_VERSION=""
BUILD_NUMBER=""

cleanup() {
  if [[ -n "$RELEASE_TEMP" && -d "$RELEASE_TEMP" ]]; then
    rm -rf -- "$RELEASE_TEMP"
  fi
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage: ./scripts/build-macos.sh [arm64|amd64] [options]

Options:
  --guest-assets DIR  Bundle DIR/vmlinux and DIR/rootfs.ext4.
                      Defaults to dist/macos-guest when present.
  --install           Install the completed bundle to ~/Applications/Dockiva.app.
  --external-only     Build without the native VMM helper and Linux guest assets.
  --release           Create a Developer ID-signed, notarized, and stapled DMG.
  --unsigned-release  Create an ad-hoc-signed DMG and updater archive.
                      macOS may block it until the user explicitly allows it.
  --sign-identity ID  Developer ID Application identity used by codesign.
                      May also be set with DOCKIVA_SIGN_IDENTITY.
  --notary-profile ID Keychain profile created by notarytool store-credentials.
                      May also be set with DOCKIVA_NOTARY_PROFILE.
  -h, --help          Show this help.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    arm64|amd64)
      ARCH="$1"
      shift
      ;;
    --guest-assets)
      [[ $# -ge 2 ]] || { echo "--guest-assets requires a directory" >&2; exit 2; }
      GUEST_ASSETS="$2"
      shift 2
      ;;
    --install)
      INSTALL=true
      shift
      ;;
    --external-only)
      EXTERNAL_ONLY=true
      shift
      ;;
    --release)
      RELEASE=true
      shift
      ;;
    --unsigned-release)
      UNSIGNED_RELEASE=true
      shift
      ;;
    --sign-identity)
      [[ $# -ge 2 ]] || { echo "--sign-identity requires an identity" >&2; exit 2; }
      SIGN_IDENTITY="$2"
      shift 2
      ;;
    --notary-profile)
      [[ $# -ge 2 ]] || { echo "--notary-profile requires a profile name" >&2; exit 2; }
      NOTARY_PROFILE="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "$RELEASE" == true && "$UNSIGNED_RELEASE" == true ]]; then
  echo "--release and --unsigned-release cannot be used together." >&2
  exit 2
fi

if [[ "$RELEASE" == true ]]; then
  if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "Release signing and notarization must run on macOS." >&2
    exit 1
  fi
  if [[ -z "$SIGN_IDENTITY" ]]; then
    echo "--release requires --sign-identity or DOCKIVA_SIGN_IDENTITY." >&2
    exit 2
  fi
  if [[ -z "$NOTARY_PROFILE" ]]; then
    echo "--release requires --notary-profile or DOCKIVA_NOTARY_PROFILE." >&2
    exit 2
  fi
  for tool in codesign ditto security xcrun; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      echo "$tool is required for a release build." >&2
      exit 1
    fi
  done
  SIGNING_IDENTITIES="$(security find-identity -v -p codesigning)"
  if [[ "$SIGNING_IDENTITIES" != *"$SIGN_IDENTITY"* ]]; then
    echo "Signing identity was not found in the current keychain: $SIGN_IDENTITY" >&2
    echo "Install the Developer ID Application certificate and private key first." >&2
    exit 1
  fi
fi

if ! command -v wails3 >/dev/null 2>&1; then
  echo "wails3 is required." >&2
  echo "Install the project version with:" >&2
  echo "  go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.19" >&2
  exit 1
fi

APP_VERSION="$(tr -d '[:space:]' < VERSION)"
if [[ ! "$APP_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
  echo "VERSION must contain a semantic version such as 0.1.0: $APP_VERSION" >&2
  exit 2
fi

BUILD_NUMBER="${DOCKIVA_BUILD_NUMBER:-}"
if [[ -z "$BUILD_NUMBER" ]]; then
  BUILD_NUMBER="$(git rev-list --count HEAD 2>/dev/null || echo 1)"
fi
if [[ ! "$BUILD_NUMBER" =~ ^[0-9]+$ ]]; then
  echo "DOCKIVA_BUILD_NUMBER must be numeric: $BUILD_NUMBER" >&2
  exit 2
fi

GENERATED_PLIST="build/.generated/Info.plist"
mkdir -p "$(dirname "$GENERATED_PLIST")"
cp -f build/darwin/Info.plist "$GENERATED_PLIST"
if [[ "$(uname -s)" == "Darwin" ]]; then
  /usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $APP_VERSION" "$GENERATED_PLIST"
  /usr/libexec/PlistBuddy -c "Set :CFBundleVersion $BUILD_NUMBER" "$GENERATED_PLIST"
fi

cp -f assets/dockiva_icon2.png build/appicon.png
if [[ "$(uname -s)" == "Darwin" ]]; then
  swift -module-cache-path build/.swift-module-cache scripts/generate-macos-brand-assets.swift
fi

if [[ -z "$GUEST_ASSETS" && -f dist/macos-guest/vmlinux && -f dist/macos-guest/rootfs.ext4 ]]; then
  GUEST_ASSETS="dist/macos-guest"
fi

if [[ "$EXTERNAL_ONLY" == false ]]; then
  if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "A native-engine macOS build must run on macOS because dockiva-vmm links Virtualization.framework." >&2
    echo "Use --external-only for an unsigned cross-build." >&2
    exit 1
  fi

  if [[ -z "$GUEST_ASSETS" || ! -f "$GUEST_ASSETS/vmlinux" || ! -f "$GUEST_ASSETS/rootfs.ext4" ]]; then
    echo "Native macOS guest assets are required." >&2
    echo "Generate them on Linux with scripts/build-macos-guest-assets-linux.sh, then pass:" >&2
    echo "  --guest-assets /path/to/macos-guest" >&2
    echo "Use --external-only to build without Dockiva Native." >&2
    exit 1
  fi

  GUEST_ASSETS="$(cd "$GUEST_ASSETS" && pwd)"
  ./scripts/build-macos-vmm.sh
fi

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "Configuring Wails cross-build tooling..."
  wails3 task setup:docker
fi

echo "Building and packaging Dockiva $APP_VERSION ($BUILD_NUMBER) for macOS/$ARCH..."
rm -rf -- bin/dockiva.app
wails3 task darwin:package ARCH="$ARCH" INFO_PLIST="$GENERATED_PLIST"

SOURCE_APP="bin/dockiva.app"
if [[ ! -d "$SOURCE_APP" ]]; then
  echo "Expected Wails bundle was not created at $SOURCE_APP" >&2
  exit 1
fi
RESOURCES="$SOURCE_APP/Contents/Resources"   # also used by Docker-only (--external-only) release signing

if [[ "$EXTERNAL_ONLY" == false ]]; then
  mkdir -p "$RESOURCES/guest"

  cp -f native/macos/DockivaVMM/.build/release/dockiva-vmm "$RESOURCES/dockiva-vmm"
  chmod 0755 "$RESOURCES/dockiva-vmm"
  cp -f "$GUEST_ASSETS/vmlinux" "$RESOURCES/guest/vmlinux"
  # Keep the sparse 2 GiB disk compressed inside the app. Wails' updater has
  # a 2 GiB total extraction cap, so bundling rootfs.ext4 directly makes an
  # otherwise small updater ZIP exceed that cap. Dockiva expands it once into
  # Application Support when the native engine is first started.
  gzip -c "$GUEST_ASSETS/rootfs.ext4" > "$RESOURCES/guest/rootfs.ext4.gz"
  chmod 0644 "$RESOURCES/guest/rootfs.ext4.gz"
  if [[ -f "$GUEST_ASSETS/manifest.txt" ]]; then
    cp -f "$GUEST_ASSETS/manifest.txt" "$RESOURCES/guest/manifest.txt"
  fi
fi

if [[ "$RELEASE" == true ]]; then
  if [[ "$EXTERNAL_ONLY" == false ]]; then
    codesign \
      --force \
      --options runtime \
      --timestamp \
      --sign "$SIGN_IDENTITY" \
      --entitlements native/macos/DockivaVMM/dockiva-vmm.entitlements \
      "$RESOURCES/dockiva-vmm"
  fi

  # Notarization requires every nested executable to carry the Developer ID
  # signature, hardened runtime and a timestamp. The native runtime also needs
  # the Hypervisor.framework entitlement.
  ENGINE_BUILD="$RESOURCES/dockiva-engine/_internal/build"
  if [[ -d "$RESOURCES/dockiva-engine" ]]; then
    for helper in "$RESOURCES/dockiva-engine/dockivad" "$RESOURCES/dockiva-engine/dockiva-engine" \
                  "$ENGINE_BUILD/dockiva-runtime" "$ENGINE_BUILD/dockiva-worker"; do
      codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$helper"
    done
    if [[ -f "$ENGINE_BUILD/dockiva-hvf" ]]; then
      codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" \
        --entitlements build/darwin/dockiva-hvf.entitlements.plist "$ENGINE_BUILD/dockiva-hvf"
    fi
  fi

  codesign \
    --force \
    --options runtime \
    --timestamp \
    --sign "$SIGN_IDENTITY" \
    "$SOURCE_APP"
  codesign --verify --deep --strict --verbose=2 "$SOURCE_APP"

  RELEASE_TEMP="$(mktemp -d "${TMPDIR:-/tmp}/dockiva-release.XXXXXX")"
  ditto -c -k --keepParent "$SOURCE_APP" "$RELEASE_TEMP/dockiva.zip"
  xcrun notarytool submit "$RELEASE_TEMP/dockiva.zip" \
    --keychain-profile "$NOTARY_PROFILE" \
    --wait
  xcrun stapler staple "$SOURCE_APP"
  xcrun stapler validate "$SOURCE_APP"
elif [[ "$EXTERNAL_ONLY" == false ]]; then
  codesign \
    --force \
    --sign - \
    --timestamp=none \
    --entitlements native/macos/DockivaVMM/dockiva-vmm.entitlements \
    "$RESOURCES/dockiva-vmm"
  codesign --force --sign - --timestamp=none "$SOURCE_APP"
fi

DIST_DIR="dist/macos-$ARCH"
DIST_APP="$DIST_DIR/Dockiva.app"
mkdir -p "$DIST_DIR"
rm -rf -- "$DIST_APP"
ditto "$SOURCE_APP" "$DIST_APP"

if [[ "$INSTALL" == true ]]; then
  INSTALL_DIR="$HOME/Applications"
  INSTALL_APP="$INSTALL_DIR/Dockiva.app"
  mkdir -p "$INSTALL_DIR"
  rm -rf -- "$INSTALL_APP"
  ditto "$DIST_APP" "$INSTALL_APP"
  echo "Installed: $INSTALL_APP"
fi

if [[ "$RELEASE" == true || "$UNSIGNED_RELEASE" == true ]]; then
  echo "Creating release DMG..."
  wails3 task darwin:create:dmg
  RELEASE_DMG="bin/dockiva.dmg"
  if [[ ! -f "$RELEASE_DMG" ]]; then
    echo "Expected release DMG was not created at $RELEASE_DMG" >&2
    exit 1
  fi

  if [[ "$RELEASE" == true ]]; then
    codesign --force --timestamp --sign "$SIGN_IDENTITY" "$RELEASE_DMG"
    codesign --verify --verbose=2 "$RELEASE_DMG"
    xcrun notarytool submit "$RELEASE_DMG" \
      --keychain-profile "$NOTARY_PROFILE" \
      --wait
    xcrun stapler staple "$RELEASE_DMG"
    xcrun stapler validate "$RELEASE_DMG"
  fi
  VERSIONED_DMG="$DIST_DIR/Dockiva-$APP_VERSION-macOS-$ARCH.dmg"
  cp -f "$RELEASE_DMG" "$VERSIONED_DMG"

  UPDATE_ARCHIVE="$DIST_DIR/Dockiva-$APP_VERSION-darwin-$ARCH.zip"
  rm -f -- "$UPDATE_ARCHIVE"
  ditto -c -k --keepParent "$DIST_APP" "$UPDATE_ARCHIVE"
  UPDATE_UNCOMPRESSED_BYTES="$(zipinfo -t "$UPDATE_ARCHIVE" | awk '{print $3}')"
  if [[ ! "$UPDATE_UNCOMPRESSED_BYTES" =~ ^[0-9]+$ ]]; then
    echo "Could not determine updater archive's uncompressed size." >&2
    exit 1
  fi
  if (( UPDATE_UNCOMPRESSED_BYTES > 2147483648 )); then
    echo "Updater archive exceeds Wails' 2 GiB extraction limit: $UPDATE_UNCOMPRESSED_BYTES bytes" >&2
    exit 1
  fi
  (
    cd "$DIST_DIR"
    shasum -a 256 "$(basename "$UPDATE_ARCHIVE")" > SHA256SUMS
  )
fi

echo
echo "Built: $DIST_APP"
if [[ "$EXTERNAL_ONLY" == false ]]; then
  echo "Included: dockiva-vmm, vmlinux, rootfs.ext4"
  echo "The guest assets are copied to Application Support on first native-engine start."
else
  echo "External Docker-only build; native VM assets were not included."
fi
if [[ "$RELEASE" == true || "$UNSIGNED_RELEASE" == true ]]; then
  echo "Release DMG: $VERSIONED_DMG"
  echo "Automatic update: $UPDATE_ARCHIVE"
  echo "Update checksum: $DIST_DIR/SHA256SUMS"
  if [[ "$RELEASE" == true ]]; then
    echo "Developer ID signing, notarization, and stapling completed."
  else
    echo "Unsigned release created with ad-hoc application signatures."
  fi
else
  echo "This development bundle is ad-hoc signed. Use --release for public distribution."
fi
