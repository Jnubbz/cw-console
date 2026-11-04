// CW Console for the Mac: a native window around the app's own page.
//
// The program that does the work (decoders, keying, CAT, logging) is the Go engine,
// Contents/MacOS/cw-decoder. This shell starts it, reads the address it prints, and shows
// that page in a WebKit window: a Dock icon, menus, no browser. Closing the window (or
// Quit) stops the engine with SIGTERM, which unkeys the radio first. If the engine exits
// by itself (its own Quit button, or restarting into an update) the shell quits too.
//
// Built by scripts/build.sh on a Mac (swiftc); see packaging/mac/Info.plist.

import Cocoa
import WebKit

final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate, WKUIDelegate,
    WKNavigationDelegate, WKDownloadDelegate
{
    var window: NSWindow!
    var web: WKWebView!
    var engine: Process?
    var engineOut = ""
    var pageURL: URL?
    var quitting = false

    func applicationDidFinishLaunching(_ note: Notification) {
        buildMenus()
        let config = WKWebViewConfiguration()
        config.preferences.javaScriptCanOpenWindowsAutomatically = false
        config.mediaTypesRequiringUserActionForPlayback = []
        web = WKWebView(frame: .zero, configuration: config)
        web.uiDelegate = self
        web.navigationDelegate = self
        web.setValue(false, forKey: "drawsBackground") // no white flash: the page is dark

        window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 1100, height: 860),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered, defer: false)
        window.title = "CW Console"
        window.minSize = NSSize(width: 720, height: 520)
        window.backgroundColor = NSColor(red: 0x12 / 255.0, green: 0x1a / 255.0, blue: 0x17 / 255.0, alpha: 1)
        window.contentView = web
        window.delegate = self
        window.setFrameAutosaveName("CWConsoleMain")
        if !window.setFrameUsingName("CWConsoleMain") { window.center() }
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        startEngine()
    }

    // MARK: the engine

    func startEngine() {
        let exe = Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/cw-decoder")
        let p = Process()
        p.executableURL = exe
        p.arguments = ["-nobrowser"]
        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = FileHandle.standardError
        pipe.fileHandleForReading.readabilityHandler = { [weak self] h in
            let d = h.availableData
            guard !d.isEmpty, let s = String(data: d, encoding: .utf8) else { return }
            DispatchQueue.main.async { self?.engineSaid(s) }
        }
        p.terminationHandler = { [weak self] _ in
            DispatchQueue.main.async {
                guard let self = self else { return }
                if self.pageURL == nil && !self.quitting {
                    self.fail("The CW Console engine stopped before it started.\n\n\(self.engineOut)")
                } else {
                    NSApp.terminate(nil) // its Quit button, or restarting into an update
                }
            }
        }
        do {
            try p.run()
            engine = p
        } catch {
            fail("Couldn't start the CW Console engine: \(error.localizedDescription)")
            return
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 15) { [weak self] in
            if let self = self, self.pageURL == nil, !self.quitting {
                self.fail("The CW Console engine didn't answer.\n\n\(self.engineOut)")
            }
        }
    }

    // The engine prints "CW Decoder running at http://localhost:7373/".
    func engineSaid(_ s: String) {
        engineOut += s
        guard pageURL == nil,
            let r = engineOut.range(of: #"https?://(localhost|127\.0\.0\.1):[0-9]+/"#, options: .regularExpression),
            let url = URL(string: String(engineOut[r]))
        else { return }
        pageURL = url
        web.load(URLRequest(url: url))
    }

    func fail(_ msg: String) {
        let a = NSAlert()
        a.messageText = "CW Console"
        a.informativeText = msg
        a.alertStyle = .critical
        a.runModal()
        quitting = true
        NSApp.terminate(nil)
    }

    func stopEngine() {
        guard let p = engine, p.isRunning else { return }
        p.terminate() // SIGTERM: the engine unkeys and closes its ports
        let deadline = Date().addingTimeInterval(3)
        while p.isRunning && Date() < deadline { usleep(50_000) }
        if p.isRunning { kill(p.processIdentifier, SIGKILL) }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ app: NSApplication) -> Bool { true }

    func applicationWillTerminate(_ note: Notification) {
        quitting = true
        stopEngine()
    }

    // MARK: page features a browser would provide

    // Microphone (the rig's USB audio): the page asks; allow it for our own page only.
    @available(macOS 12.0, *)
    func webView(
        _ webView: WKWebView, requestMediaCapturePermissionFor origin: WKSecurityOrigin,
        initiatedByFrame frame: WKFrameInfo, type: WKMediaCaptureType,
        decisionHandler: @escaping (WKPermissionDecision) -> Void
    ) {
        decisionHandler(origin.host == pageURL?.host ? .grant : .deny)
    }

    func webView(
        _ webView: WKWebView, runJavaScriptAlertPanelWithMessage message: String,
        initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping () -> Void
    ) {
        let a = NSAlert()
        a.messageText = message
        a.beginSheetModal(for: window) { _ in completionHandler() }
    }

    func webView(
        _ webView: WKWebView, runJavaScriptConfirmPanelWithMessage message: String,
        initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping (Bool) -> Void
    ) {
        let a = NSAlert()
        a.messageText = message
        a.addButton(withTitle: "OK")
        a.addButton(withTitle: "Cancel")
        a.beginSheetModal(for: window) { r in completionHandler(r == .alertFirstButtonReturn) }
    }

    func webView(
        _ webView: WKWebView, runJavaScriptTextInputPanelWithPrompt prompt: String,
        defaultText: String?, initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping (String?) -> Void
    ) {
        let a = NSAlert()
        a.messageText = prompt
        a.addButton(withTitle: "OK")
        a.addButton(withTitle: "Cancel")
        let field = NSTextField(frame: NSRect(x: 0, y: 0, width: 280, height: 24))
        field.stringValue = defaultText ?? ""
        a.accessoryView = field
        a.window.initialFirstResponder = field
        a.beginSheetModal(for: window) { r in
            completionHandler(r == .alertFirstButtonReturn ? field.stringValue : nil)
        }
    }

    // <input type=file>: Decode WAV file...
    func webView(
        _ webView: WKWebView, runOpenPanelWith parameters: WKOpenPanelParameters,
        initiatedByFrame frame: WKFrameInfo, completionHandler: @escaping ([URL]?) -> Void
    ) {
        let p = NSOpenPanel()
        p.allowsMultipleSelection = parameters.allowsMultipleSelection
        p.canChooseDirectories = false
        p.beginSheetModal(for: window) { r in completionHandler(r == .OK ? p.urls : nil) }
    }

    // Links out (GitHub, QRZ...) open in the real browser; our page stays here.
    func webView(
        _ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
        decisionHandler: @escaping (WKNavigationActionPolicy) -> Void
    ) {
        if action.shouldPerformDownload {
            decisionHandler(.download)
            return
        }
        if let url = action.request.url, let page = pageURL,
            url.host != page.host || url.port != page.port, url.scheme == "http" || url.scheme == "https"
        {
            NSWorkspace.shared.open(url)
            decisionHandler(.cancel)
            return
        }
        decisionHandler(.allow)
    }

    // Pages that open a new window (target=_blank): same rule.
    func webView(
        _ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
        for action: WKNavigationAction, windowFeatures: WKWindowFeatures
    ) -> WKWebView? {
        if let url = action.request.url { NSWorkspace.shared.open(url) }
        return nil
    }

    // Save... and log files: downloads go to ~/Downloads, then show in Finder.
    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        download.delegate = self
    }

    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        download.delegate = self
    }

    func webView(
        _ webView: WKWebView, decidePolicyFor response: WKNavigationResponse,
        decisionHandler: @escaping (WKNavigationResponsePolicy) -> Void
    ) {
        decisionHandler(response.canShowMIMEType ? .allow : .download)
    }

    func download(
        _ download: WKDownload, decideDestinationUsing response: URLResponse,
        suggestedFilename: String, completionHandler: @escaping (URL?) -> Void
    ) {
        let dir = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask)[0]
        var url = dir.appendingPathComponent(suggestedFilename)
        var n = 1
        while FileManager.default.fileExists(atPath: url.path) {
            let base = (suggestedFilename as NSString).deletingPathExtension
            let ext = (suggestedFilename as NSString).pathExtension
            url = dir.appendingPathComponent("\(base) (\(n))" + (ext.isEmpty ? "" : ".\(ext)"))
            n += 1
        }
        completionHandler(url)
        DispatchQueue.main.asyncAfter(deadline: .now() + 1) {
            NSWorkspace.shared.activateFileViewerSelecting([url])
        }
    }

    // If the engine restarts (it changes its security code), reload.
    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) { webView.reload() }

    // MARK: menus (the Edit menu is what makes Copy and Paste work in text boxes)

    func buildMenus() {
        let main = NSMenu()
        let appItem = NSMenuItem()
        main.addItem(appItem)
        let app = NSMenu()
        app.addItem(withTitle: "About CW Console", action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)), keyEquivalent: "")
        app.addItem(.separator())
        app.addItem(withTitle: "Hide CW Console", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        app.addItem(.separator())
        app.addItem(withTitle: "Quit CW Console", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        appItem.submenu = app

        let editItem = NSMenuItem()
        main.addItem(editItem)
        let edit = NSMenu(title: "Edit")
        edit.addItem(withTitle: "Undo", action: Selector(("undo:")), keyEquivalent: "z")
        edit.addItem(withTitle: "Redo", action: Selector(("redo:")), keyEquivalent: "Z")
        edit.addItem(.separator())
        edit.addItem(withTitle: "Cut", action: #selector(NSText.cut(_:)), keyEquivalent: "x")
        edit.addItem(withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
        edit.addItem(withTitle: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v")
        edit.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
        editItem.submenu = edit

        let viewItem = NSMenuItem()
        main.addItem(viewItem)
        let view = NSMenu(title: "View")
        view.addItem(withTitle: "Reload", action: #selector(reloadPage), keyEquivalent: "r")
        view.addItem(withTitle: "Actual Size", action: #selector(zoomReset), keyEquivalent: "0")
        view.addItem(withTitle: "Zoom In", action: #selector(zoomIn), keyEquivalent: "+")
        view.addItem(withTitle: "Zoom Out", action: #selector(zoomOut), keyEquivalent: "-")
        viewItem.submenu = view

        let winItem = NSMenuItem()
        main.addItem(winItem)
        let win = NSMenu(title: "Window")
        win.addItem(withTitle: "Minimize", action: #selector(NSWindow.performMiniaturize(_:)), keyEquivalent: "m")
        win.addItem(withTitle: "Zoom", action: #selector(NSWindow.performZoom(_:)), keyEquivalent: "")
        winItem.submenu = win
        NSApp.windowsMenu = win
        NSApp.mainMenu = main
    }

    @objc func reloadPage() { web.reload() }
    @objc func zoomReset() { web.pageZoom = 1 }
    @objc func zoomIn() { web.pageZoom = min(2, web.pageZoom + 0.1) }
    @objc func zoomOut() { web.pageZoom = max(0.6, web.pageZoom - 0.1) }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.regular)
app.run()
