//go:build amd64

package deepcw

func cpuid(eaxArg, ecxArg uint32) (eax, ebx, ecx, edx uint32)
func xgetbv() (eax, edx uint32)

//go:noescape
func sgemmKernel4x16(a *float32, lda int, b *float32, ldb int, c *float32, ldc int, k int)

// hasAVX2 is true when the CPU has AVX2 + FMA and the OS saves the YMM registers.
var hasAVX2 = detectAVX2()

// useAVX2 selects the vector kernel (SetSIMD can turn it off for comparison).
var useAVX2 = hasAVX2

func detectAVX2() bool {
	maxLeaf, _, _, _ := cpuid(0, 0)
	if maxLeaf < 7 {
		return false
	}
	_, _, c1, _ := cpuid(1, 0)
	fma := c1&(1<<12) != 0
	osxsave := c1&(1<<27) != 0
	avx := c1&(1<<28) != 0
	if !(fma && osxsave && avx) {
		return false
	}
	if xcr0, _ := xgetbv(); xcr0&6 != 6 { // OS must preserve XMM and YMM state
		return false
	}
	_, b7, _, _ := cpuid(7, 0)
	return b7&(1<<5) != 0
}

// SIMDName describes the active matrix kernel.
func SIMDName() string {
	if useAVX2 {
		return "AVX2"
	}
	if hasAVX2 {
		return "portable (AVX2 off)"
	}
	return "portable (no AVX2 on this CPU)"
}

// SetSIMD enables or disables the AVX2 kernel (only takes effect if the CPU has it).
func SetSIMD(on bool) { useAVX2 = on && hasAVX2 }

// HasAVX2 reports CPU support.
func HasAVX2() bool { return hasAVX2 }
