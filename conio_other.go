//go:build !windows

package main

import (
	"fmt"
	"strings"
)

// 非 Windows 平台的降级实现：不支持原始按键与光标操作，
// 仅保证代码可在其它平台编译运行。

func init() {
	useCursor = false
}

func readKey() (Key, error) {
	r, _, err := fallbackStdin.ReadRune()
	if err != nil {
		return Key{Kind: KeyNone}, err
	}
	return classifyFallbackRune(r), nil
}

func windowSize() (int, int) {
	return 0, 0
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

var _ = fmt.Sprintf
var _ = strings.Repeat
