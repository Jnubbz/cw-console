#include "textflag.h"

// func cpuid(eaxArg, ecxArg uint32) (eax, ebx, ecx, edx uint32)
TEXT ·cpuid(SB), NOSPLIT, $0-24
	MOVL eaxArg+0(FP), AX
	MOVL ecxArg+4(FP), CX
	CPUID
	MOVL AX, eax+8(FP)
	MOVL BX, ebx+12(FP)
	MOVL CX, ecx+16(FP)
	MOVL DX, edx+20(FP)
	RET

// func xgetbv() (eax, edx uint32)
TEXT ·xgetbv(SB), NOSPLIT, $0-8
	MOVL $0, CX
	XGETBV
	MOVL AX, eax+0(FP)
	MOVL DX, edx+4(FP)
	RET

// func sgemmKernel4x16(a *float32, lda int, b *float32, ldb int, c *float32, ldc int, k int)
//
// C[0:4][0:16] += A[0:4][0:k] × B[0:k][0:16]
// Eight YMM accumulators (4 rows × 2×8 columns) stay in registers for the whole k loop:
// each step loads one 16-wide row of B, broadcasts one A value per row, and issues 8 FMAs.
TEXT ·sgemmKernel4x16(SB), NOSPLIT, $0-56
	MOVQ a+0(FP), SI
	MOVQ lda+8(FP), R8
	SHLQ $2, R8
	MOVQ b+16(FP), DI
	MOVQ ldb+24(FP), R9
	SHLQ $2, R9
	MOVQ c+32(FP), DX
	MOVQ ldc+40(FP), R10
	SHLQ $2, R10
	MOVQ k+48(FP), CX

	LEAQ (SI)(R8*1), R11
	LEAQ (R11)(R8*1), R12
	LEAQ (R12)(R8*1), R13

	VXORPS Y0, Y0, Y0
	VXORPS Y1, Y1, Y1
	VXORPS Y2, Y2, Y2
	VXORPS Y3, Y3, Y3
	VXORPS Y4, Y4, Y4
	VXORPS Y5, Y5, Y5
	VXORPS Y6, Y6, Y6
	VXORPS Y7, Y7, Y7

	TESTQ CX, CX
	JE    store

loop:
	VMOVUPS      (DI), Y8
	VMOVUPS      32(DI), Y9
	VBROADCASTSS (SI), Y10
	VFMADD231PS  Y8, Y10, Y0
	VFMADD231PS  Y9, Y10, Y1
	VBROADCASTSS (R11), Y11
	VFMADD231PS  Y8, Y11, Y2
	VFMADD231PS  Y9, Y11, Y3
	VBROADCASTSS (R12), Y12
	VFMADD231PS  Y8, Y12, Y4
	VFMADD231PS  Y9, Y12, Y5
	VBROADCASTSS (R13), Y13
	VFMADD231PS  Y8, Y13, Y6
	VFMADD231PS  Y9, Y13, Y7
	ADDQ         $4, SI
	ADDQ         $4, R11
	ADDQ         $4, R12
	ADDQ         $4, R13
	ADDQ         R9, DI
	DECQ         CX
	JNZ          loop

store:
	VADDPS  (DX), Y0, Y0
	VMOVUPS Y0, (DX)
	VADDPS  32(DX), Y1, Y1
	VMOVUPS Y1, 32(DX)
	ADDQ    R10, DX
	VADDPS  (DX), Y2, Y2
	VMOVUPS Y2, (DX)
	VADDPS  32(DX), Y3, Y3
	VMOVUPS Y3, 32(DX)
	ADDQ    R10, DX
	VADDPS  (DX), Y4, Y4
	VMOVUPS Y4, (DX)
	VADDPS  32(DX), Y5, Y5
	VMOVUPS Y5, 32(DX)
	ADDQ    R10, DX
	VADDPS  (DX), Y6, Y6
	VMOVUPS Y6, (DX)
	VADDPS  32(DX), Y7, Y7
	VMOVUPS Y7, 32(DX)
	VZEROUPPER
	RET
