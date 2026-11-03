//go:build windows

package main

// Minimal Win32 bindings via syscall (no third-party modules).

import (
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	comctl32 = syscall.NewLazyDLL("comctl32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	winmm    = syscall.NewLazyDLL("winmm.dll")

	pRegisterClassExW     = user32.NewProc("RegisterClassExW")
	pCreateWindowExW      = user32.NewProc("CreateWindowExW")
	pDefWindowProcW       = user32.NewProc("DefWindowProcW")
	pGetMessageW          = user32.NewProc("GetMessageW")
	pTranslateMessage     = user32.NewProc("TranslateMessage")
	pDispatchMessageW     = user32.NewProc("DispatchMessageW")
	pIsDialogMessageW     = user32.NewProc("IsDialogMessageW")
	pPostQuitMessage      = user32.NewProc("PostQuitMessage")
	pSendMessageW         = user32.NewProc("SendMessageW")
	pSetTimer             = user32.NewProc("SetTimer")
	pInvalidateRect       = user32.NewProc("InvalidateRect")
	pBeginPaint           = user32.NewProc("BeginPaint")
	pEndPaint             = user32.NewProc("EndPaint")
	pGetClientRect        = user32.NewProc("GetClientRect")
	pMoveWindow           = user32.NewProc("MoveWindow")
	pLoadCursorW          = user32.NewProc("LoadCursorW")
	pLoadIconW            = user32.NewProc("LoadIconW")
	pMessageBoxW          = user32.NewProc("MessageBoxW")
	pSetWindowTextW       = user32.NewProc("SetWindowTextW")
	pGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	pGetWindowTextW       = user32.NewProc("GetWindowTextW")
	pShowWindow           = user32.NewProc("ShowWindow")
	pUpdateWindow         = user32.NewProc("UpdateWindow")
	pFillRect             = user32.NewProc("FillRect")
	pGetDC                = user32.NewProc("GetDC")
	pReleaseDC            = user32.NewProc("ReleaseDC")
	pSetProcessDPIAware   = user32.NewProc("SetProcessDPIAware")
	pEnableWindow         = user32.NewProc("EnableWindow")

	pCreateFontW            = gdi32.NewProc("CreateFontW")
	pCreateSolidBrush       = gdi32.NewProc("CreateSolidBrush")
	pCreatePen              = gdi32.NewProc("CreatePen")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pDeleteDC               = gdi32.NewProc("DeleteDC")
	pSetBkMode              = gdi32.NewProc("SetBkMode")
	pSetBkColor             = gdi32.NewProc("SetBkColor")
	pSetTextColor           = gdi32.NewProc("SetTextColor")
	pTextOutW               = gdi32.NewProc("TextOutW")
	pMoveToEx               = gdi32.NewProc("MoveToEx")
	pLineTo                 = gdi32.NewProc("LineTo")
	pGetDeviceCaps          = gdi32.NewProc("GetDeviceCaps")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	pCreateActCtxW    = kernel32.NewProc("CreateActCtxW")
	pActivateActCtx   = kernel32.NewProc("ActivateActCtx")

	pInitCommonControlsEx = comctl32.NewProc("InitCommonControlsEx")
	pGetOpenFileNameW     = comdlg32.NewProc("GetOpenFileNameW")
	pGetSaveFileNameW     = comdlg32.NewProc("GetSaveFileNameW")
	pDragAcceptFiles      = shell32.NewProc("DragAcceptFiles")
	pDragQueryFileW       = shell32.NewProc("DragQueryFileW")
	pDragFinish           = shell32.NewProc("DragFinish")

	pWaveInGetNumDevs      = winmm.NewProc("waveInGetNumDevs")
	pWaveInGetDevCapsW     = winmm.NewProc("waveInGetDevCapsW")
	pWaveInOpen            = winmm.NewProc("waveInOpen")
	pWaveInPrepareHeader   = winmm.NewProc("waveInPrepareHeader")
	pWaveInUnprepareHeader = winmm.NewProc("waveInUnprepareHeader")
	pWaveInAddBuffer       = winmm.NewProc("waveInAddBuffer")
	pWaveInStart           = winmm.NewProc("waveInStart")
	pWaveInReset           = winmm.NewProc("waveInReset")
	pWaveInClose           = winmm.NewProc("waveInClose")
)

const (
	WS_OVERLAPPEDWINDOW = 0x00CF0000
	WS_CHILD            = 0x40000000
	WS_VISIBLE          = 0x10000000
	WS_VSCROLL          = 0x00200000
	WS_TABSTOP          = 0x00010000
	WS_CLIPCHILDREN     = 0x02000000
	WS_EX_CLIENTEDGE    = 0x00000200
	CW_USEDEFAULT       = 0x80000000

	ES_MULTILINE   = 0x0004
	ES_AUTOVSCROLL = 0x0040
	ES_READONLY    = 0x0800
	ES_NOHIDESEL   = 0x0100

	CBS_DROPDOWNLIST = 0x0003
	CBS_HASSTRINGS   = 0x0200
	BS_PUSHBUTTON    = 0x0000
	BS_AUTOCHECKBOX  = 0x0003
	BM_GETCHECK      = 0x00F0
	BM_SETCHECK      = 0x00F1
	SS_LEFT          = 0x0000
	SS_NOTIFY        = 0x0100
	TBS_HORZ         = 0x0000
	TBS_NOTICKS      = 0x0010

	WM_CREATE          = 0x0001
	WM_DESTROY         = 0x0002
	WM_SIZE            = 0x0005
	WM_PAINT           = 0x000F
	WM_CLOSE           = 0x0010
	WM_ERASEBKGND      = 0x0014
	WM_GETMINMAXINFO   = 0x0024
	WM_SETFONT         = 0x0030
	WM_COMMAND         = 0x0111
	WM_TIMER           = 0x0113
	WM_HSCROLL         = 0x0114
	WM_CTLCOLOREDIT    = 0x0133
	WM_CTLCOLORSTATIC  = 0x0138
	WM_LBUTTONDOWN     = 0x0201
	WM_MOUSEMOVE       = 0x0200
	WM_DROPFILES       = 0x0233
	WM_COPY            = 0x0301
	WM_USER            = 0x0400
	EM_SETSEL          = 0x00B1
	EM_REPLACESEL      = 0x00C2
	EM_SETLIMITTEXT    = 0x00C5
	CB_ADDSTRING       = 0x0143
	CB_GETCURSEL       = 0x0147
	CB_SETCURSEL       = 0x014E
	CBN_SELCHANGE      = 1
	TBM_GETPOS         = WM_USER
	TBM_SETPOS         = WM_USER + 5
	TBM_SETRANGE       = WM_USER + 6
	TBM_SETPAGESIZE    = WM_USER + 21
	TBM_SETLINESIZE    = WM_USER + 23
	SW_SHOW            = 5
	IDC_ARROW          = 32512
	IDI_APPLICATION    = 32512
	COLOR_BTNFACE      = 15
	TRANSPARENT        = 1
	PS_SOLID           = 0
	SRCCOPY            = 0x00CC0020
	LOGPIXELSY         = 90
	MB_OK              = 0
	MB_ICONINFORMATION = 0x40
	MB_ICONWARNING     = 0x30

	OFN_FILEMUSTEXIST   = 0x00001000
	OFN_PATHMUSTEXIST   = 0x00000800
	OFN_OVERWRITEPROMPT = 0x00000002
	OFN_NOCHANGEDIR     = 0x00000008

	WAVE_MAPPER     = 0xFFFFFFFF
	WAVE_FORMAT_PCM = 1
	WHDR_DONE       = 0x00000001
)

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type POINT struct{ X, Y int32 }

type MSG struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
	_       uint32
}

type RECT struct{ Left, Top, Right, Bottom int32 }

type PAINTSTRUCT struct {
	Hdc         uintptr
	FErase      int32
	RcPaint     RECT
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

type MINMAXINFO struct {
	PtReserved     POINT
	PtMaxSize      POINT
	PtMaxPosition  POINT
	PtMinTrackSize POINT
	PtMaxTrackSize POINT
}

type INITCOMMONCONTROLSEX struct {
	DwSize uint32
	DwICC  uint32
}

type OPENFILENAMEW struct {
	LStructSize       uint32
	HwndOwner         uintptr
	HInstance         uintptr
	LpstrFilter       *uint16
	LpstrCustomFilter *uint16
	NMaxCustFilter    uint32
	NFilterIndex      uint32
	LpstrFile         *uint16
	NMaxFile          uint32
	LpstrFileTitle    *uint16
	NMaxFileTitle     uint32
	LpstrInitialDir   *uint16
	LpstrTitle        *uint16
	Flags             uint32
	NFileOffset       uint16
	NFileExtension    uint16
	LpstrDefExt       *uint16
	LCustData         uintptr
	LpfnHook          uintptr
	LpTemplateName    *uint16
	PvReserved        uintptr
	DwReserved        uint32
	FlagsEx           uint32
}

type ACTCTXW struct {
	CbSize                 uint32
	DwFlags                uint32
	LpSource               *uint16
	WProcessorArchitecture uint16
	WLangId                uint16
	LpAssemblyDirectory    *uint16
	LpResourceName         *uint16
	LpApplicationName      *uint16
	HModule                uintptr
}

type WAVEFORMATEX struct {
	WFormatTag      uint16
	NChannels       uint16
	NSamplesPerSec  uint32
	NAvgBytesPerSec uint32
	NBlockAlign     uint16
	WBitsPerSample  uint16
	CbSize          uint16
}

type WAVEHDR struct {
	LpData          uintptr
	DwBufferLength  uint32
	DwBytesRecorded uint32
	DwUser          uintptr
	DwFlags         uint32
	DwLoops         uint32
	LpNext          uintptr
	Reserved        uintptr
}

type WAVEINCAPSW struct {
	WMid           uint16
	WPid           uint16
	VDriverVersion uint32
	SzPname        [32]uint16
	DwFormats      uint32
	WChannels      uint16
	WReserved1     uint16
}

func u16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func rgb(r, g, b uint8) uintptr { return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16 }

func send(h uintptr, msg uint32, w, l uintptr) uintptr {
	r, _, _ := pSendMessageW.Call(h, uintptr(msg), w, l)
	return r
}

// ptrSink forces every struct we hand to Windows onto the heap: Go heap objects never
// move, but a Go stack can be copied (e.g. while a modal dialog runs our timer
// callback), which would leave Windows holding a stale pointer.
var ptrSink any

func ptr[T any](p *T) uintptr {
	ptrSink = p
	return uintptr(unsafe.Pointer(p))
}

// sendStr sends a message whose LPARAM is a string. The conversion happens inside the
// Call argument list, so the string stays alive for the duration of the call.
func sendStr(h uintptr, msg uint32, w uintptr, s string) uintptr {
	r, _, _ := pSendMessageW.Call(h, uintptr(msg), w, uintptr(unsafe.Pointer(u16(s))))
	return r
}

func loword(x uintptr) uint32 { return uint32(x) & 0xFFFF }
func hiword(x uintptr) uint32 { return (uint32(x) >> 16) & 0xFFFF }

func messageBox(owner uintptr, text, title string, flags uintptr) {
	pMessageBoxW.Call(owner, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(title))), flags)
}

func windowText(h uintptr) string {
	n, _, _ := pGetWindowTextLengthW.Call(h)
	buf := make([]uint16, n+1)
	pGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return syscall.UTF16ToString(buf)
}

func setText(h uintptr, s string) {
	pSetWindowTextW.Call(h, uintptr(unsafe.Pointer(u16(s))))
}
