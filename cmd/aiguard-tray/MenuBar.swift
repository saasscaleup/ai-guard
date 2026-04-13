import Cocoa
import Foundation

class AppDelegate: NSObject, NSApplicationDelegate {
    var statusItem: NSStatusItem!
    var statusMenuItem: NSMenuItem!
    var alertsMenuItem: NSMenuItem!
    var killMenuItem: NSMenuItem!
    var timer: Timer!

    // MARK: - Icon helpers

    /// Updates the menu bar icon to a small coloured SF Symbol shield.
    /// Using squareLength (22 pt) keeps it compact so it is never pushed
    /// off the menu bar when other icons are present.
    func setIcon(symbolName: String, color: NSColor) {
        guard let button = statusItem.button else { return }
        let cfg = NSImage.SymbolConfiguration(pointSize: 14, weight: .medium)
        if let img = NSImage(systemSymbolName: symbolName, accessibilityDescription: nil)?
                        .withSymbolConfiguration(cfg) {
            let tinted = img.copy() as! NSImage
            tinted.lockFocus()
            color.set()
            NSRect(origin: .zero, size: tinted.size).fill(using: .sourceAtop)
            tinted.unlockFocus()
            tinted.isTemplate = false
            button.image = tinted
            button.title = ""
        }
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        // Use squareLength so the icon is always 22 pt — never overflows the menu bar
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        setIcon(symbolName: "shield.fill", color: .systemYellow)

        // Build menu
        let menu = NSMenu()

        statusMenuItem = NSMenuItem(title: "Connecting to daemon...", action: nil, keyEquivalent: "")
        statusMenuItem.isEnabled = false
        menu.addItem(statusMenuItem)
        menu.addItem(NSMenuItem.separator())

        // ── Terminate All ─────────────────────────────────────────────
        killMenuItem = NSMenuItem(title: "⛔  Terminate All AI Processes", action: #selector(killAll), keyEquivalent: "k")
        killMenuItem.target = self
        menu.addItem(killMenuItem)
        menu.addItem(NSMenuItem.separator())

        alertsMenuItem = NSMenuItem(title: "🔔  Alerts: none", action: #selector(openAlerts), keyEquivalent: "")
        alertsMenuItem.target = self
        menu.addItem(alertsMenuItem)
        menu.addItem(NSMenuItem.separator())

        let dashItem = NSMenuItem(title: "🌐  Open Dashboard", action: #selector(openDashboard), keyEquivalent: "d")
        dashItem.target = self
        menu.addItem(dashItem)
        menu.addItem(NSMenuItem.separator())

        let quit = NSMenuItem(title: "Quit AIGuard", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        menu.addItem(quit)

        statusItem.menu = menu

        // Poll every 2 seconds
        timer = Timer.scheduledTimer(withTimeInterval: 2.0, repeats: true) { _ in self.refresh() }
        refresh()
    }

    // MARK: - Actions

    @objc func killAll() {
        guard let url = URL(string: "http://127.0.0.1:7474/kill") else { return }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.timeoutInterval = 3
        URLSession.shared.dataTask(with: req) { data, _, _ in
            DispatchQueue.main.async {
                let count = (try? JSONSerialization.jsonObject(with: data ?? Data()) as? [String: Any])
                    .flatMap { $0["count"] as? Int } ?? 0
                self.killMenuItem.title = count > 0 ? "✅  Terminated \(count) process(es)" : "✅  Nothing to terminate"
                DispatchQueue.main.asyncAfter(deadline: .now() + 3) {
                    self.killMenuItem.title = "⛔  Terminate All AI Processes"
                }
                self.refresh()
            }
        }.resume()
    }

    @objc func openDashboard() {
        NSWorkspace.shared.open(URL(string: "http://127.0.0.1:7474/")!)
    }

    @objc func openAlerts() {
        NSWorkspace.shared.open(URL(string: "http://127.0.0.1:7474/")!)
    }

    // MARK: - Status polling

    func refresh() {
        fetchStatus()
        fetchAlerts()
    }

    func fetchStatus() {
        guard let url = URL(string: "http://127.0.0.1:7474/status") else { return }
        var req = URLRequest(url: url)
        req.timeoutInterval = 2
        URLSession.shared.dataTask(with: req) { data, _, error in
            DispatchQueue.main.async {
                guard let data = data,
                      let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                      let count = json["count"] as? Int,
                      let procs = json["ai_processes"] as? [[String: Any]] else {
                    self.setIcon(symbolName: "shield.slash.fill", color: .systemGray)
                    self.statusMenuItem.title = "⚠️  Daemon not running — start with: aiguard daemon"
                    return
                }
                let killable = procs.filter { $0["will_kill"] as? Bool == true }.count
                if killable > 0 {
                    self.setIcon(symbolName: "shield.fill", color: .systemRed)
                    self.statusMenuItem.title = "🔴  \(count) process(es) — \(killable) terminable"
                } else if count > 0 {
                    self.setIcon(symbolName: "shield.fill", color: .systemYellow)
                    self.statusMenuItem.title = "🟡  \(count) process(es) — monitoring only"
                } else {
                    self.setIcon(symbolName: "shield.fill", color: .systemGreen)
                    self.statusMenuItem.title = "✅  No AI processes running"
                }
            }
        }.resume()
    }

    func fetchAlerts() {
        guard let url = URL(string: "http://127.0.0.1:7474/alerts") else { return }
        var req = URLRequest(url: url)
        req.timeoutInterval = 2
        URLSession.shared.dataTask(with: req) { data, _, _ in
            DispatchQueue.main.async {
                guard let data = data,
                      let alerts = try? JSONSerialization.jsonObject(with: data) as? [[String: Any]] else { return }
                if alerts.isEmpty {
                    self.alertsMenuItem.title = "🔔  Alerts: none"
                } else {
                    self.alertsMenuItem.title = "🚨  \(alerts.count) Alert(s) — click to view"
                }
            }
        }.resume()
    }
}

// Entry point
let app = NSApplication.shared
app.setActivationPolicy(.accessory) // menu bar only — no Dock icon
let delegate = AppDelegate()
app.delegate = delegate
app.run()
