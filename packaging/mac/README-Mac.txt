CW DECODER for Mac (Apple Silicon)
==================================

The same decoders (classic + DeepCW AI) and CW sender as the Windows version.
The app runs in the background; its window is a page in your web browser
(Chrome, Safari or Edge). Nothing to install besides the app.


FIRST RUN
---------
1. Unzip, and drag "CW Decoder.app" to your Applications folder.

2. The app isn't signed by Apple, so macOS will refuse to open it the first
   time (it may even call it "damaged"; it isn't). Open Terminal and run:

       xattr -dr com.apple.quarantine "/Applications/CW Decoder.app"

   Then double-click it. (Alternatively, if macOS offers it: System Settings >
   Privacy & Security > "Open Anyway". If you have an Apple Developer ID,
   sign-and-notarize.sh does it properly - see the notes inside it.)

3. Your browser opens at http://localhost:7373 with the CW Decoder page.
   Bookmark it if you like.

4. Press Start and pick the radio's audio input. The FT-710's USB audio
   usually shows up as "USB Audio CODEC". The browser asks for microphone
   permission the first time; allow it. If it can't open the input, check
   System Settings > Privacy & Security > Microphone has your browser on.

Keep the CW Decoder tab open while you use it (its own browser window is
handy). Quit with the Quit button. If you just close the tab, the app
quits by itself about a minute later.


THE AI DECODER ON A MAC
-----------------------
Apple Silicon doesn't have Intel's AVX2 instructions, so the AI uses the
plain-math path (the Windows version's fast vector code is Intel-only). An
M-series chip has plenty of cores, so Fast or Max are still very usable.
Press "Benchmark..." for the real numbers on your Mac.


SENDING CW
----------
Same as on Windows: the app keys the rig by switching the DTR or RTS line of
one of the FT-710's USB serial ports.

- Rig menu: PC KEYING = DTR (or RTS). Pick the same line in the app.
- Key port: the FT-710 makes two serial ports on a Mac, named something like
  cu.SLAB_USBtoUART / cu.SLAB_USBtoUART2, or cu.usbserial-XXXX. One is for
  rig control (CAT), the other for keying. If nothing keys, try the other port
  or the other line. The (refresh) button re-scans after plugging in.
- Quit Nexus or WSJT-X first if they have that port open.
- Safe first test: break-in (BK-IN) OFF, tick Transmit enabled, type. You
  should hear the sidetone only. Then BK-IN on, low power first.
- Transmit is always off when the app starts.

Keys: Mac F-keys are media keys unless you hold fn, so F1-F8 macros are
fn+F1..F8, or Option+1..8. Esc stops sending instantly. If the page is
closed while sending, sending stops within 5 seconds.

Macros: "Edit macros..." opens them right in the page. They're stored in
~/Library/Application Support/CW Decoder/macros.txt (settings live there too).


OPERATING AIDS (Auto-CQ, auto-reply, logging...)
------------------------------------------------
These sit just under the F-key buttons.

Heard:  Callsigns the decoder copies show up as buttons, newest first. Click
        one to put it in the Call box. "Them: 18 WPM" is the other station's
        measured speed.

Auto-CQ:  Tick it and it sends your CQ macro (pick which one; F1 or F2 for
        POTA), listens for the number of seconds you set, and calls again.
        It holds off while it's copying someone on the frequency.
        It pauses as soon as anyone answers (a sign-off like E E, 73 or TU,
        or static, doesn't count), or when you type, enter a call,
        or press an F-key - from then on it's your turn. After you log the
        contact (see {LOG} below) it goes back to calling. If a contact
        fizzles out (nothing sent or heard for about 20 s) it carries on too.
        Esc, or switching transmit off, stops it completely.

Auto-reply:  When someone answers your CQ (Auto-CQ or one you sent yourself)
        and they've finished sending, it puts their call in the Call box and
        sends your reply macro (F3 "Exch" by default) once. It only answers
        when exactly one clear callsign was copied: with two callers, or
        no clear call, it leaves it to you and says so. It never logs or
        sends TU on its own. If the decoder got the call slightly wrong, the
        other station will correct you, as they would with a person.
        With the AI decoder, it replies about 3-5 s after the caller stops
        (the AI's own delay); with the classic decoder, about 2 s.

Match speed:  Sends at the other station's measured speed instead of the WPM
        slider, kept between the two limits you set. When nobody has been
        heard for a minute and a half, the slider speed is used.

Logging:  Type the frequency in Freq (14.058, 14058 or just 20m), and the band
        shows next to it. "Log QSO" logs the call in the Call box, then clears
        it. Easier: put {LOG} in a macro and the contact is logged when you
        send it. Your TU and 73 macros (F4 and F8) now end with {LOG}
        (added automatically if you never changed them). "Undo" removes the
        last contact. A call you've already worked on that band today
        shows DUPE next to the Call box.

        Each contact records the call, UTC date and time (when you first sent
        their call), band, frequency, CW, the RST you sent (RST in the macros)
        and the RST they sent you (picked out of the decoded text; 599 if
        none was copied), your call, state and grid.

        For POTA, set PARK = US-1234 (your park) in the macros. "Log folder"
        opens ~/Library/Application Support/CW Decoder/Logs,
        which then also holds the file the POTA uploader wants, named like
        KN0BLE@US-1234-20261013.adi (one per park per UTC day), with your
        park as MY_SIG_INFO. Every contact also goes into
        cw-decoder-log.adi, which any logging program can import.


SECURITY NOTE
-------------
The app only listens on this Mac (localhost), checks that requests come from
its own page, and requires a random code that changes every launch, so other
websites you visit can't talk to it (or key your transmitter).


LICENSES
--------
GPL-3.0 (LICENSE.txt). Decoder and keying logic from Nexus by KD9TAW.
The DeepCW model (inside the app, Contents/Resources/deepcw) is by e04,
AGPL-3.0.
