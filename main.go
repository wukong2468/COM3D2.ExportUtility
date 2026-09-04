package main

import (
	"fmt"
	"os"
	"strings"
	"unicode"
)

// termY 模拟 Console.CursorTop：跟踪当前文本光标所在行（0 基）。
// 所有“输出一行”的调用都必须通过 outLine/outBlank 以保持计数一致。
var termY int

func outLine(s string) {
	fmt.Println(s)
	termY++
}

func outLineColor(s, color string) {
	fmt.Print(color, s, "\n", ansiReset)
	termY++
}

func outBlank(n int) {
	for i := 0; i < n; i++ {
		fmt.Println()
		termY++
	}
}

func cursorTo(row, col int) {
	if !useCursor {
		return // 输出被重定向时不做光标定位，避免把 ANSI 序列混入管道输出
	}
	fmt.Print(moveTo(row, col))
}

func main() {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}

	// ========== 1. 标题与当前程序的工作目录 ==========
	outLineColor("COM3D2 资源提取工具", ansiCyan)
	outLineColor(strings.Repeat("─", 26), ansiCyan)
	outLine(fmt.Sprintf("工作目录：%s", wd))
	outBlank(1)

	// ========== 2. 要提取的文件类型（多选列表） ==========
	// 选项值：* 表示提取全部；.ks/.nei 按后缀匹配；audio/image 为内容匹配
	// （音频识别 .ogg/.wav/... 与 KCES AudioClip；图像识别 .tex/.png/... 与 KCES Texture2D/Sprite）
	extOptionValues := []string{"*", ".ks", ".nei", "audio", "image"}
	extOptionLabels := []string{"任意类型", ".ks", ".nei", "音频（.ogg）", "图像（.tex → .png）"}
	extSelected := make([]bool, len(extOptionValues))
	extIndex := 0

	outLine("要提取的文件类型（↑/↓ 移动，【空格】多选，【回车】确认）：")
	outLine("（选中“任意类型”时，将提取 .arc 包内的全部文件）")
	extListTop := termY

	renderExtList := func() {
		for i := 0; i < len(extOptionValues); i++ {
			cursorMark := " "
			if i == extIndex {
				cursorMark = ">"
			}
			check := "[ ]"
			if extSelected[i] {
				check = "[x]"
			}
			cursorTo(extListTop+i, 0)
			fmt.Print(cursorMark + " " + check + " " + extOptionLabels[i] + "\n")
		}
		termY = extListTop + len(extOptionValues)
	}

	renderExtList()

	extDone := false
	for !extDone {
		k, err := readKey()
		if err != nil {
			break
		}
		switch k.Kind {
		case KeyUp:
			if extIndex > 0 {
				extIndex--
				renderExtList()
			}
		case KeyDown:
			if extIndex < len(extOptionValues)-1 {
				extIndex++
				renderExtList()
			}
		case KeySpace:
			extSelected[extIndex] = !extSelected[extIndex]
			renderExtList()
		case KeyEnter:
			extDone = true
		}
	}
	outBlank(1)

	var selectedLabels []string
	var selectedValues []string
	for i := 0; i < len(extOptionValues); i++ {
		if extSelected[i] {
			selectedValues = append(selectedValues, extOptionValues[i])
			selectedLabels = append(selectedLabels, extOptionLabels[i])
		}
	}
	selectedExtensions := selectedValues

	labelParts := make([]string, 0, len(selectedLabels))
	for _, l := range selectedLabels {
		labelParts = append(labelParts, "【"+l+"】")
	}
	selectedExts := strings.Join(labelParts, " ")

	if len(selectedExts) == 0 {
		outLineColor("已选择的文件类型：（未选择）", ansiYellow)
	} else {
		outLine(fmt.Sprintf("已选择的文件类型：%s", selectedExts))
	}
	outBlank(1)

	// ========== 3. 文件导出路径输入框 ==========

	// 带默认值的行编辑输入：默认填入 ExportUtilityResources，可直接回车使用，
	// 也支持 ←/→ 移动光标、Backspace/Delete 删除、直接输入字符进行修改。
	readLineWithDefault := func(defaultValue string, startCol int) string {
		chars := []rune(defaultValue)
		caret := len(chars)
		top := termY
		maxWidth := runeDisplayWidth(chars, len(chars))
		if maxWidth < 1 {
			maxWidth = 1
		}

		render := func() {
			cursorTo(top, startCol)
			s := string(chars)
			w := displayWidth(s)
			if w < maxWidth {
				fmt.Print(s + strings.Repeat(" ", maxWidth-w))
			} else {
				fmt.Print(s)
			}
			cursorTo(top, startCol+runeDisplayWidth(chars, caret))
		}

		render()

		for {
			key, err := readKey()
			if err != nil {
				return string(chars)
			}
			switch key.Kind {
			case KeyEnter:
				fmt.Println()
				termY++
				return string(chars)
			case KeyLeft:
				if caret > 0 {
					caret--
					render()
				}
			case KeyRight:
				if caret < len(chars) {
					caret++
					render()
				}
			case KeyBackspace:
				if caret > 0 {
					chars = append(chars[:caret-1], chars[caret:]...)
					caret--
					render()
				}
			case KeyDelete:
				if caret < len(chars) {
					chars = append(chars[:caret], chars[caret+1:]...)
					render()
				}
			default:
				if key.Kind == KeyRune && !unicode.IsControl(key.Rune) {
					chars = append(chars[:caret], append([]rune{key.Rune}, chars[caret:]...)...)
					caret++
					if w := displayWidth(string(chars)); w > maxWidth {
						maxWidth = w
					}
					render()
				}
			}
		}
	}

	promptPath := "文件导出到："
	fmt.Print(promptPath)
	exportDir := readLineWithDefault("ExportUtilityResources", displayWidth(promptPath))
	exportDir = strings.TrimSpace(exportDir)
	outBlank(1)

	// ========== 4. 覆盖旧文件（复选框） ==========
	overwrite := false
	outLine("覆盖旧文件（【空格】切换，【回车】确认）：")
	cbTop := termY

	renderCheckbox := func() {
		cursorTo(cbTop, 0)
		line := "[ ] 覆盖旧文件"
		if overwrite {
			line = "[x] 覆盖旧文件"
		}
		fmt.Print(line + "\n")
		termY = cbTop + 1
	}

	renderCheckbox()
	cbDone := false
	for !cbDone {
		k, err := readKey()
		if err != nil {
			break
		}
		switch k.Kind {
		case KeySpace:
			overwrite = !overwrite
			renderCheckbox()
		case KeyEnter:
			cbDone = true
		}
	}
	outBlank(1)

	// ========== 5. 取消 / 开始 按钮 ==========
	buttons := []string{"取消", "开始"}
	btnIndex := 0

	outLine("（↑/↓ 切换按钮，【回车】确认）")
	btnTop := termY

	// 按钮纵向排列：取消 在上，开始 在下
	renderButtons := func() {
		for i := 0; i < len(buttons); i++ {
			mark := " "
			if i == btnIndex {
				mark = ">"
			}
			cursorTo(btnTop+i, 0)
			if i == btnIndex {
				fmt.Print(ansiBtnSel, mark+" "+buttons[i], ansiReset, "\n")
			} else {
				fmt.Print(mark+" "+buttons[i], "\n")
			}
		}
		termY = btnTop + len(buttons)
	}

	renderButtons()
	btnDone := false
	for !btnDone {
		k, err := readKey()
		if err != nil {
			break
		}
		switch k.Kind {
		case KeyUp:
			if btnIndex > 0 {
				btnIndex--
				renderButtons()
			}
		case KeyDown:
			if btnIndex < len(buttons)-1 {
				btnIndex++
				renderButtons()
			}
		case KeyEnter:
			btnDone = true
		}
	}
	outBlank(2)

	// ========== 执行结果 ==========
	if btnIndex == 0 { // 点击“取消”
		outLineColor("⚠ 已取消，退出程序。", ansiYellow)
		return
	}

	// 执行提取任务：内部直接调用 MeidoSerialization 源码完成
	// arc 解析（替代 CM3D2.ToolKit）与 .nei/.tex 转换（替代 exe 子进程）。
	extractor := newExtractor(wd, exportDir, selectedExtensions, overwrite)
	if !extractor.run() {
		outLineColor("✗ 提取任务未能开始，请根据上方错误信息调整后重试。", ansiRed)
		return
	}
}
