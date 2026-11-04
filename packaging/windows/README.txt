CW CONSOLE for Windows
======================

A standalone Morse (CW) decoder and sender with two decoders:

  - Classic: the CW decoder from Nexus by KD9TAW (https://github.com/kd9taw/nexus),
    with optional speed/fade fixes. Tiny CPU use.
  - AI: the DeepCW neural-network decoder (model by e04,
    https://github.com/e04/deepcw-engine), the same model Nexus uses. Much better on
    weak, fading, fast or irregular signals, at the cost of more CPU.

No install needed. Just run CW-Console.exe. Keep the "deepcw" folder next to it;
that's the AI model.


FIRST RUN
---------
1. Unzip the folder anywhere (Desktop is fine) and double-click CW-Console.exe.

2. Windows may show "Windows protected your PC" because the program isn't
   code-signed. Click "More info", then "Run anyway". (Some antivirus tools
   are also wary of new unsigned programs; the full source code is included.)

3. If nothing is heard: Windows Settings > Privacy & security > Microphone >
   turn on "Let desktop apps access your microphone". The decoder listens to
   your radio through a Windows recording device, which Windows treats as a mic.


CONNECTING YOUR RADIO
---------------------
- Most modern rigs (IC-7300, FT-991A, FT-710, etc.) have a built-in USB sound
  card. With the USB cable connected it shows up in the "Input" list as
  something like "Microphone (USB Audio CODEC)". Pick it.
- Older rigs: run the speaker or headphone output into a USB sound dongle or
  your PC's line-in, and pick that input.
- Put the rig in CW mode with a fairly narrow filter (250-500 Hz is ideal).


TUNING
------
Both decoders listen around one audio pitch, shown by the orange line on the
spectrum strip. That pitch must match the tone you hear:

  - Click the signal in the spectrum strip, or
  - Press "Auto tune" while a signal is coming in, or
  - Set the slider to your rig's CW pitch setting (many rigs default to
    600 Hz; on an IC-7300 it's in the CW pitch menu).

The KEY light flashes with the incoming keying when it's locked on. The shaded
band shows what the selected decoder is listening to.


THE AI DECODER
--------------
Pick "AI - DeepCW neural net" in the Decoder box.

- It decodes 15-second stretches of audio, so text starts about 15 seconds after
  you start listening. After that it arrives in short bursts, roughly 2 to 4
  seconds behind the signal depending on the speed setting.
- "Focus on pitch" (on by default) makes the AI listen only around the orange
  line. Without it, the AI hears everything from 400 to 1200 Hz and will mix in
  a nearby station. Keep the pitch between about 450 and 1150 Hz for the AI.
- Sensitivity and the "Improved tracking" box only affect the classic decoder.

AI speed (how much CPU it may use):
  Auto      Eco on battery, Balanced when plugged in (default)
  Eco       1 core, new text every ~4 s or slower. Easy on a laptop battery.
  Balanced  1 core, every ~2 s (what Nexus does)
  Fast      all cores, every ~1 s
  Max       all cores, as often as the computer can manage

Press "Benchmark..." to see exactly what each setting costs on this computer:
how long one decode takes, how far behind the text will run, and the CPU load.

The AI uses AVX2 vector instructions, which most PCs from about 2015 on have
(including 7th-gen Core i5s). On older CPUs it still works, just several times
slower. The benchmark says which math it is using.


DECODING RECORDINGS
-------------------
Click "Decode WAV file..." or drag a .wav file onto the window. It uses the
selected decoder, and the pitch is found automatically. The "samples" folder
has test recordings: clean 20 WPM, a noisy QSO, a fast 35 WPM QSO, and a QSO
with a second station 250 Hz away.

cwdecode-file.exe does the same from a command prompt:
    cwdecode-file samples\02-noisy-qso-22wpm.wav
    cwdecode-file -ai samples\04-qso-with-qrm.wav
    cwdecode-file -pitch 700 -sens 0.6 myrecording.wav
    cwdecode-file -nexus myrecording.wav      (exact Nexus classic behaviour)


SENDING CW
----------
The Sender section (bottom of the window; the "Sender" box hides it) keys your
rig through its USB cable by switching the DTR or RTS line of one of the rig's
COM ports. No extra hardware.

Set up the FT-710 (or other Yaesu) once:
  1. Rig menu: set PC KEYING to DTR (or RTS). On Yaesu rigs of this generation
     it's in the CW settings; check your manual for the exact spot.
  2. In "Key port", pick the rig's Standard COM Port. The FT-710's USB cable
     makes two ports, "Enhanced" (normally CAT/rig control) and "Standard";
     keying normally goes through the Standard one. The list shows the names
     Windows uses. If nothing keys, try the other port or the other line.
  3. Pick the same line (DTR/RTS) as the rig menu.
  4. Close Nexus, WSJT-X or a logger if they hold that port open.

Safe first test: turn break-in (BK-IN) OFF, tick "Transmit enabled" and type
something. You should hear the rig's sidetone keying but it won't transmit.
When that sounds right, turn BK-IN on (try low power or a dummy load first).

Using it:
  - Type in the long box. "Send as I type": characters go out as you type,
    and anything not yet sent can still be backspaced. "Send on Enter": compose
    a line, press Enter to send it.
  - The strip above shows sent text in grey and what's still queued in amber.
  - F1-F8 (or the buttons) send macros. Put the other station's call in the
    Call box; macros insert it with {CALL}.
  - Esc, or "Stop (Esc)", stops instantly and clears anything still queued.
  - WPM slider sets speed.
  - What you send is echoed in the transcript as "TX>" lines. Both decoders
    are muted while you transmit so they don't copy your own signal.
  - Transmit is always OFF when the program starts.

Macros: "Edit macros..." opens macros.txt in Notepad. Save it and the buttons
update within a couple of seconds. It starts with your call and some POTA-style
macros. Tokens: {MYCALL} {NAME} {MYSTATE} {MYGRID} {RST} (sent as 5NN),
{CALL}, plus any KEY = value you add (e.g. PARK = US-1234, then use {PARK}).


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
        Short on screen space? The "Ops" box at the bottom hides these rows.

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
        opens %AppData%\CW Console\Logs,
        which then also holds the file the POTA uploader wants, named like
        KN0BLE@US-1234-20261013.adi (one per park per UTC day), with your
        park as MY_SIG_INFO. Every contact also goes into
        cw-decoder-log.adi, which any logging program can import.


TEST RESULTS
------------
On 60 random test recordings (12-40 WPM, noise, fading, and in a quarter of
them a second station 150-400 Hz away), characters copied correctly:
    Classic decoder (improved):   82% on average
    AI decoder:                   97% - better on 32 recordings, worse on none

On 120 random recordings without a second station:
    Original Nexus classic:       67%
    Improved classic:             89%
Static with no signal: the improved classic prints no more junk than the
original.

On the 2-core test computer, one AI decode took 0.6 s; "Balanced" used about
15% of the CPU and "Eco" about 8%. Your Benchmark button gives the real
numbers for your machine.


GOOD TO KNOW
------------
- Each decoder copies one signal at a time (the one at the orange line).
- Settings (pitch, decoder, AI speed, input, key port, WPM...) are remembered
  in %AppData%\CW Console\settings.ini; macros are in macros.txt there too,
  and the logs in the Logs folder.
- Keying timing: in testing, 95% of elements were within about 1 ms of perfect
  at 20 and 30 WPM. A very busy PC can occasionally stretch a single element.
- The AI decoder's engine was written for this app (no extra libraries). It is
  checked against the plain-math version on every run of the test suite.


LICENSES
--------
CW Console: GPL-3.0 (LICENSE.txt). The classic decoder and the AI audio handling
are from Nexus by KD9TAW, GPL-3.0.
The DeepCW model in the deepcw folder is by e04 and licensed AGPL-3.0
(deepcw\LICENSE). Using it yourself is fine; if you ever redistribute the app or
offer it as a service, the AGPL's source-sharing terms apply to it.
