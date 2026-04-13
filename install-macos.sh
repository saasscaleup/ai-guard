#!/bin/bash

# macOS installation script for AIGuard
# Installs pre-built binaries to /Applications/AIGuard.app
# Does NOT rebuild — use deploy-macos.sh for full build+install

set -e

if [ ! -f aiguard ] || [ ! -f aiguard-tray ]; then
    echo "❌ Binaries not found. Run deploy-macos.sh first (builds aiguard and aiguard-tray)"
    exit 1
fi

echo "📦 Installing AIGuard to /Applications..."
APP="/Applications/AIGuard.app"
mkdir -p "$APP/Contents/MacOS"

# Write Info.plist (required for macOS to treat this as a proper app bundle)
cat > "$APP/Contents/Info.plist" << 'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleExecutable</key><string>AIGuard</string>
    <key>CFBundleIdentifier</key><string>com.aiguard.menubar</string>
    <key>CFBundleName</key><string>AIGuard</string>
    <key>CFBundleVersion</key><string>1.0</string>
    <key>LSUIElement</key><true/>
</dict>
</plist>
EOF

cp aiguard-tray "$APP/Contents/MacOS/AIGuard"
chmod +x "$APP/Contents/MacOS/AIGuard"

# Clear quarantine on the bundle AND the binary inside it
xattr -c "$APP" 2>/dev/null || true
xattr -c "$APP/Contents/MacOS/AIGuard" 2>/dev/null || true

# Re-register with Launch Services so macOS picks up the new binary
/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -f "$APP" 2>/dev/null || true

echo "✓ Installation complete: $APP"
