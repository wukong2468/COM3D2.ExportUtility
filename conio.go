package main

import (
	"bufio"
	"os"
	"strconv"
)

// ==================== 跨平台控制台能力检测与按键类型 ====================

// KeyKind 描述一次按键的种类。
type KeyKind int

const (
	KeyNone      KeyKind = iota // 无意义/忽略的按键
	KeyUp                       // ↑
	KeyDown                     // ↓
	KeyLeft                     // ←
	KeyRight                    // →
	KeyEnter                    // 回车
	KeySpace                    // 空格
	KeyBackspace                // 退格
	KeyDelete                   // Delete
	KeyRune                     // 普通可打印字符
)

// Key 是一次原始按键事件。
type Key struct {
	Kind KeyKind
	Rune rune // Kind == KeyRune 时有效
}

// useCursor 表示控制台未被重定向，可以使用光标定位/窗口操作（对应 C# 的 !Console.IsOutputRedirected）。
var useCursor bool

// fallbackStdin 在 stdin 不是真实控制台（管道/重定向）时按字节读取降级处理。
var fallbackStdin = bufio.NewReader(os.Stdin)

// ==================== ANSI 转义序列 ====================

const (
	ansiReset  = "\x1b[0m"
	ansiCyan   = "\x1b[36m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
	ansiWhite  = "\x1b[37m"
	ansiBtnSel = "\x1b[107;30m" // 灰色背景 + 黑色文字（对应 C# 按钮选中态）
)

// moveTo 返回把光标移到 (row, col) 的 ANSI 序列（row/col 为 0 基，内部转 1 基）。
// 终端自身会按“显示宽度”（全角=2）管理列位置，因此调用方只需用显示宽度计算 col。
func moveTo(row, col int) string {
	return "\x1b[" + strconv.Itoa(row+1) + ";" + strconv.Itoa(col+1) + "H"
}

// clearLine 返回清除光标所在整行的 ANSI 序列。
func clearLine() string {
	return "\x1b[2K"
}

// ==================== 显示宽度（全角字符按 2 列） ====================

// displayWidth 计算字符串在控制台中的显示宽度（全角字符按 2 列计算）。
func displayWidth(s string) int {
	w := 0
	for _, c := range s {
		w += displayWidthRune(c)
	}
	return w
}

// runeDisplayWidth 计算 rune 切片前缀 [0:n) 的显示宽度。
func runeDisplayWidth(runes []rune, n int) int {
	w := 0
	for i := 0; i < n; i++ {
		w += displayWidthRune(runes[i])
	}
	return w
}

func displayWidthRune(c rune) int {
	if isWideRune(c) {
		return 2
	}
	return 1
}

// isWideRune 判断字符是否为全角（东亚宽）字符，范围与 C# 版本一致。
func isWideRune(c rune) bool {
	return c == 0x3000 || c == 0xFF06 ||
		(c >= 0x2E80 && c <= 0x9FFF) ||
		(c >= 0xAC00 && c <= 0xD7A3) ||
		(c >= 0xF900 && c <= 0xFAFF) ||
		(c >= 0xFE30 && c <= 0xFE4F) ||
		(c >= 0xFF00 && c <= 0xFF60) ||
		(c >= 0xFFE0 && c <= 0xFFE6)
}
