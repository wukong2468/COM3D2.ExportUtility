//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ==================== Windows 控制台底层封装 ====================
// C# 版用 Console 类完成这些操作；Go 标准库不暴露它们，
// 因此这里直接调用 kernel32 控制台 API。

var kernel32 = windows.NewLazySystemDLL("kernel32.dll")

var (
	procGetStdHandle               = kernel32.NewProc("GetStdHandle")
	procGetConsoleMode             = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procReadConsoleInputW          = kernel32.NewProc("ReadConsoleInputW")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
	procSetConsoleOutputCP         = kernel32.NewProc("SetConsoleOutputCP")
	procSetConsoleCP               = kernel32.NewProc("SetConsoleCP")
)

const (
	stdInputHandle  = uint32(0xFFFFFFF6) // -10
	stdOutputHandle = uint32(0xFFFFFFF5) // -11

	enableProcessedInput = 0x0001
	enableLineInput      = 0x0002
	enableEchoInput      = 0x0004
	enableVT             = 0x0004 // ENABLE_VIRTUAL_TERMINAL_PROCESSING（输出）

	keyEvent = 0x0001

	vkUp     = 0x26
	vkDown   = 0x28
	vkLeft   = 0x25
	vkRight  = 0x27
	vkReturn = 0x0D
	vkSpace  = 0x20
	vkBack   = 0x08
	vkDelete = 0x2E
)

var stdInHandle = stdHandle(stdInputHandle)
var stdOutHandle = stdHandle(stdOutputHandle)

func stdHandle(n uint32) windows.Handle {
	r, _, _ := procGetStdHandle.Call(uintptr(n))
	return windows.Handle(r)
}

func isConsole(h windows.Handle) bool {
	var mode uint32
	r, _, _ := procGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode)))
	return r != 0
}

const codepageUTF8 = 65001

func init() {
	// 对应 C# 的 Console.OutputEncoding = Encoding.UTF8 / InputEncoding = Encoding.UTF8：
	// 把控制台输入/输出代码页切换成 UTF-8，保证中文显示与输入正常。
	procSetConsoleOutputCP.Call(codepageUTF8)
	procSetConsoleCP.Call(codepageUTF8)

	useCursor = isConsole(stdOutHandle)
	if useCursor {
		var mode uint32
		if r1, _, _ := procGetConsoleMode.Call(uintptr(stdOutHandle), uintptr(unsafe.Pointer(&mode))); r1 != 0 {
			// 开启 VT 处理，随后统一用 ANSI 序列做光标与着色（等价于 C# 的 Console 输出）。
			procSetConsoleMode.Call(uintptr(stdOutHandle), uintptr(mode|enableVT))
		}
	}
}

// inputRecord / keyEventRecord 对应 Win32 的 INPUT_RECORD / KEY_EVENT_RECORD 布局。
type inputRecord struct {
	eventType uint16
	_         [2]byte
	event     [16]byte
}

type keyEventRecord struct {
	bKeyDown          int32
	wRepeatCount      uint16
	wVirtualKeyCode   uint16
	wVirtualScanCode  uint16
	uchr              uint16
	dwControlKeyState uint32
}

// readKey 读取一次按键：控制台用 ReadConsoleInputW 拿到方向键/功能键，
// 非控制台（重定向输入）退化为逐字符读取 stdin。
func readKey() (Key, error) {
	if !isConsole(stdInHandle) {
		r, _, err := fallbackStdin.ReadRune()
		if err != nil {
			return Key{Kind: KeyNone}, err
		}
		return classifyFallbackRune(r), nil
	}

	var rec inputRecord
	var numRead uint32
	for {
		r1, _, e := procReadConsoleInputW.Call(
			uintptr(stdInHandle),
			uintptr(unsafe.Pointer(&rec)),
			1,
			uintptr(unsafe.Pointer(&numRead)),
		)
		if r1 == 0 {
			if e != nil {
				return Key{Kind: KeyNone}, e
			}
			return Key{Kind: KeyNone}, fmt.Errorf("ReadConsoleInputW failed")
		}
		if rec.eventType != keyEvent {
			continue
		}
		ke := (*keyEventRecord)(unsafe.Pointer(&rec.event[0]))
		if ke.bKeyDown == 0 {
			continue // 忽略按键抬起事件（对应 C# ReadKey 行为）
		}
		switch ke.wVirtualKeyCode {
		case vkUp:
			return Key{Kind: KeyUp}, nil
		case vkDown:
			return Key{Kind: KeyDown}, nil
		case vkLeft:
			return Key{Kind: KeyLeft}, nil
		case vkRight:
			return Key{Kind: KeyRight}, nil
		case vkReturn:
			return Key{Kind: KeyEnter}, nil
		case vkSpace:
			return Key{Kind: KeySpace}, nil
		case vkBack:
			return Key{Kind: KeyBackspace}, nil
		case vkDelete:
			return Key{Kind: KeyDelete}, nil
		}
		if ke.uchr != 0 && ke.uchr != '\r' {
			return Key{Kind: KeyRune, Rune: rune(ke.uchr)}, nil
		}
	}
}

func classifyFallbackRune(r rune) Key {
	switch r {
	case '\n', '\r':
		return Key{Kind: KeyEnter}
	case ' ':
		return Key{Kind: KeySpace}
	case 0x08:
		return Key{Kind: KeyBackspace}
	case 0x7F:
		return Key{Kind: KeyDelete}
	default:
		return Key{Kind: KeyRune, Rune: r}
	}
}

// windowSize 返回控制台窗口的列数/行数；失败时回退 80x25。
func windowSize() (int, int) {
	if !useCursor {
		return 0, 0
	}
	var info struct {
		dwSize              struct{ x, y int16 }
		dwCursorPosition    struct{ x, y int16 }
		wAttributes         uint16
		srWindow            struct{ l, t, r, b int16 }
		dwMaximumWindowSize struct{ x, y int16 }
	}
	r1, _, _ := procGetConsoleScreenBufferInfo.Call(uintptr(stdOutHandle), uintptr(unsafe.Pointer(&info)))
	if r1 == 0 {
		return 80, 25
	}
	return int(info.srWindow.r-info.srWindow.l) + 1, int(info.srWindow.b-info.srWindow.t) + 1
}
