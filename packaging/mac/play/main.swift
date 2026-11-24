// cw-play: CW Console's sound output on the Mac. It plays raw 16-bit PCM from stdin to one
// output device, the way aplay does on the Pi box, so the engine can key a CW tone (or play
// FT8) into the radio's USB sound card rather than the Mac's speakers.
//
//   cw-play --list                          output devices, one per line: uid<TAB>name
//   cw-play -D <uid or name> -r 48000 -c 1  play stdin (S16_LE, interleaved) to that device;
//                                           no -D: the default output. aplay's other flags
//                                           (-q, -t raw, -f S16_LE, ...) are accepted.
//
// Once the device is running it prints "ready" on stdout (the engine keys the rig by its
// own clock, so it starts that clock then). It plays until stdin closes and what it was given has been played, then exits 0. Errors
// go to stderr, exit status 1. Built by scripts/build.sh into CW Console.app/Contents/MacOS.

import AVFoundation
import CoreAudio
import Foundation

func fail(_ msg: String) -> Never {
    FileHandle.standardError.write(("cw-play: " + msg + "\n").data(using: .utf8)!)
    exit(1)
}

// MARK: devices

struct OutDev {
    let id: AudioDeviceID
    let uid: String
    let name: String
}

func address(_ sel: AudioObjectPropertySelector, _ scope: AudioObjectPropertyScope = kAudioObjectPropertyScopeGlobal)
    -> AudioObjectPropertyAddress
{
    AudioObjectPropertyAddress(mSelector: sel, mScope: scope, mElement: kAudioObjectPropertyElementMain)
}

func stringProp(_ id: AudioObjectID, _ sel: AudioObjectPropertySelector) -> String? {
    var addr = address(sel)
    var value: Unmanaged<CFString>?
    var size = UInt32(MemoryLayout<Unmanaged<CFString>?>.size)
    let st = withUnsafeMutablePointer(to: &value) { AudioObjectGetPropertyData(id, &addr, 0, nil, &size, $0) }
    guard st == noErr, let v = value else { return nil }
    return v.takeRetainedValue() as String
}

func outputChannels(_ id: AudioObjectID) -> Int {
    var addr = address(kAudioDevicePropertyStreamConfiguration, kAudioObjectPropertyScopeOutput)
    var size: UInt32 = 0
    guard AudioObjectGetPropertyDataSize(id, &addr, 0, nil, &size) == noErr, size > 0 else { return 0 }
    let raw = UnsafeMutableRawPointer.allocate(byteCount: Int(size), alignment: MemoryLayout<AudioBufferList>.alignment)
    defer { raw.deallocate() }
    guard AudioObjectGetPropertyData(id, &addr, 0, nil, &size, raw) == noErr else { return 0 }
    let list = UnsafeMutableAudioBufferListPointer(raw.assumingMemoryBound(to: AudioBufferList.self))
    var n = 0
    for b in list { n += Int(b.mNumberChannels) }
    return n
}

func outputDevices() -> [OutDev] {
    let system = AudioObjectID(kAudioObjectSystemObject)
    var addr = address(kAudioHardwarePropertyDevices)
    var size: UInt32 = 0
    guard AudioObjectGetPropertyDataSize(system, &addr, 0, nil, &size) == noErr else { return [] }
    var ids = [AudioDeviceID](repeating: 0, count: Int(size) / MemoryLayout<AudioDeviceID>.size)
    guard AudioObjectGetPropertyData(system, &addr, 0, nil, &size, &ids) == noErr else { return [] }
    var out: [OutDev] = []
    for id in ids where outputChannels(id) > 0 {
        out.append(OutDev(id: id, uid: stringProp(id, kAudioDevicePropertyDeviceUID) ?? "", name: stringProp(id, kAudioObjectPropertyName) ?? ""))
    }
    return out
}

// findDevice: by uid, then by name, then by a name that contains it (or it the name).
func findDevice(_ want: String) -> OutDev? {
    let devs = outputDevices()
    return devs.first(where: { $0.uid == want })
        ?? devs.first(where: { $0.name == want })
        ?? devs.first(where: { !$0.name.isEmpty && ($0.name.localizedCaseInsensitiveContains(want) || want.localizedCaseInsensitiveContains($0.name)) })
}

// MARK: the samples, stdin to the render callback

final class Ring {
    private var buf: [Float] = []
    private var head = 0
    private var closed = false
    private let lock = NSLock()

    func append(_ s: [Float]) {
        lock.lock()
        buf.append(contentsOf: s)
        lock.unlock()
    }

    func close() {
        lock.lock()
        closed = true
        lock.unlock()
    }

    // done: stdin has closed and everything has been handed to the device.
    var done: Bool {
        lock.lock()
        defer { lock.unlock() }
        return closed && head >= buf.count
    }

    // read copies up to n samples into out and returns how many it copied.
    func read(into out: UnsafeMutablePointer<Float>, count n: Int) -> Int {
        lock.lock()
        defer { lock.unlock() }
        let k = min(n, buf.count - head)
        if k > 0 {
            buf.withUnsafeBufferPointer { src in
                for i in 0..<k { out[i] = src[head + i] }
            }
            head += k
        }
        if head > 1 << 16 && head * 2 > buf.count { // drop what's played, now and then
            buf.removeFirst(head)
            head = 0
        }
        return k
    }
}

// MARK: main

let argv = Array(CommandLine.arguments.dropFirst())
if argv.contains("--list") {
    for d in outputDevices() { print("\(d.uid)\t\(d.name)") }
    exit(0)
}
var want = ""
var rate = 48000.0
var channels = 1
var i = 0
while i < argv.count {
    let a = argv[i]
    let next = i + 1 < argv.count ? argv[i + 1] : ""
    switch a {
    case "-D": want = next; i += 1
    case "-r": rate = Double(next) ?? 48000; i += 1
    case "-c": channels = max(1, min(8, Int(next) ?? 1)); i += 1
    case "-t", "-f": i += 1
    default: break
    }
    i += 1
}

let ring = Ring()
let engine = AVAudioEngine()
let output = engine.outputNode
if !want.isEmpty {
    guard let dev = findDevice(want) else { fail("no output device \"\(want)\"") }
    guard let au = output.audioUnit else { fail("no output unit") }
    var id = dev.id
    let st = AudioUnitSetProperty(au, kAudioOutputUnitProperty_CurrentDevice, kAudioUnitScope_Global, 0, &id,
        UInt32(MemoryLayout<AudioDeviceID>.size))
    if st != noErr { fail("couldn't use \(dev.name) (error \(st))") }
}

guard let format = AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: rate,
    channels: AVAudioChannelCount(channels), interleaved: false)
else { fail("bad format: \(rate) Hz, \(channels) channels") }

let maxFrames = 16384
let scratch = UnsafeMutablePointer<Float>.allocate(capacity: maxFrames * channels)
let source = AVAudioSourceNode(format: format) { _, _, frameCount, abl -> OSStatus in
    let bufs = UnsafeMutableAudioBufferListPointer(abl)
    let n = min(Int(frameCount), maxFrames)
    let got = ring.read(into: scratch, count: n * channels)
    for c in 0..<bufs.count {
        guard let p = bufs[c].mData?.assumingMemoryBound(to: Float.self) else { continue }
        let ch = min(c, channels - 1)
        for f in 0..<Int(frameCount) {
            let k = f * channels + ch
            p[f] = f < n && k < got ? scratch[k] : 0
        }
    }
    return noErr
}
engine.attach(source)
engine.connect(source, to: engine.mainMixerNode, format: format)
engine.connect(engine.mainMixerNode, to: output, format: output.outputFormat(forBus: 0))
engine.mainMixerNode.outputVolume = 1

// a sound card that goes away (unplugged) ends the play: the engine sees us exit
NotificationCenter.default.addObserver(forName: .AVAudioEngineConfigurationChange, object: engine, queue: nil) { _ in
    if !engine.isRunning { fail("the sound card stopped") }
}

do {
    try engine.start()
} catch {
    fail("couldn't start the sound output: \(error.localizedDescription)")
}
print("ready") // the engine times its keying from here
fflush(stdout)

let reader = Thread {
    let frameBytes = 2 * channels
    var carry = Data()
    while true {
        let d = FileHandle.standardInput.availableData
        if d.isEmpty { break }
        carry.append(d)
        let whole = (carry.count / frameBytes) * frameBytes
        if whole == 0 { continue }
        var f = [Float](repeating: 0, count: whole / 2)
        carry.withUnsafeBytes { raw in
            for k in 0..<(whole / 2) {
                f[k] = Float(Int16(littleEndian: raw.loadUnaligned(fromByteOffset: k * 2, as: Int16.self))) / 32768
            }
        }
        carry.removeFirst(whole)
        ring.append(f)
    }
    ring.close()
}
reader.start()

while !ring.done { usleep(20_000) }
usleep(300_000) // what the device still holds
engine.stop()
exit(0)
