package main

// 提取任务：搜索工作目录下所有 .arc 文件，按选择的类型过滤，
// 并把符合条件的文件提取到「导出路径」下的对应子目录中。

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MeidoPromotionAssociation/MeidoSerialization/v2/application"
	"github.com/MeidoPromotionAssociation/MeidoSerialization/v2/serialization/COM3D2"
	"github.com/MeidoPromotionAssociation/MeidoSerialization/v2/serialization/COM3D2/arc"
	COM3D2Service "github.com/MeidoPromotionAssociation/MeidoSerialization/v2/service/COM3D2"
	KCESService "github.com/MeidoPromotionAssociation/MeidoSerialization/v2/service/KCES"
)

type Extractor struct {
	workingDir    string              // 搜索 .arc 的工作目录
	exportDir     string              // 用户输入的导出路径（相对或绝对）
	fullExportDir string              // 实际导出目录：相对路径拼接工作目录，绝对路径直接使用
	extensions    map[string]struct{} // 扩展名集合（匹配时忽略大小写），如 { ".ks", ".nei" }
	extractAll    bool                // 是否提取全部文件（勾选了“任意类型”）
	overwrite     bool

	useCursor bool // 控制台是否支持光标/窗口操作

	// 控制台输出互斥锁：日志、进度条的写入都必须在同一把锁内串行完成，
	// 否则多线程提取时输出会交错错乱。注意 C# 锁可重入，Go 不行，
	// 因此带 Locked 后缀的方法要求调用方已持有 consoleMu。
	consoleMu sync.Mutex

	arcTotal       int64
	arcProcessed   int64
	extractedCount int64
	convertedCount int64
	progressActive bool
}

// newExtractor 对应 C# 的构造函数。
func newExtractor(workingDir, exportDir string, extensions []string, overwrite bool) *Extractor {
	exts := make(map[string]struct{})
	for _, e := range extensions {
		exts[strings.TrimSpace(e)] = struct{}{}
	}
	_, extractAll := exts["*"]

	fullExportDir := ""
	if e := strings.TrimSpace(exportDir); e != "" {
		if filepath.IsAbs(e) {
			fullExportDir = e
		} else {
			fullExportDir = filepath.Join(workingDir, e)
		}
	}

	return &Extractor{
		workingDir:    workingDir,
		exportDir:     strings.TrimSpace(exportDir),
		fullExportDir: fullExportDir,
		extensions:    exts,
		extractAll:    extractAll,
		overwrite:     overwrite,
		useCursor:     useCursor,
	}
}

// run 执行提取任务，返回是否成功执行（参数校验失败或发生致命错误时为 false）。
func (e *Extractor) run() bool {
	// ---------- 1. 参数校验 ----------
	if len(e.extensions) == 0 {
		e.errorLog("未选择任何要提取的文件类型，请至少勾选一种扩展名或“任意类型”后再试。")
		return false
	}
	if e.exportDir == "" {
		e.errorLog("未设置文件导出路径，请在“文件导出到”中填写导出文件夹后再试。")
		return false
	}

	// ---------- 2. 测试创建导出文件夹 ----------
	if err := os.MkdirAll(e.fullExportDir, 0755); err != nil {
		e.errorLog(fmt.Sprintf("无法创建导出文件夹：%s\n原因：%v", e.fullExportDir, err))
		return false
	}
	e.infoLog(fmt.Sprintf("导出文件夹：%s", e.fullExportDir))

	// ---------- 3. 搜索工作目录下所有 .arc 文件 ----------
	start := time.Now()
	arcFiles := e.searchArcFiles()
	e.arcTotal = int64(len(arcFiles))
	if e.arcTotal == 0 {
		e.infoLog("未找到任何 .arc 文件，无需提取。")
		return true
	}

	// ---------- 4. 并行处理 .arc 文件 ----------
	maxParallelism := runtime.NumCPU()
	if maxParallelism < 1 {
		maxParallelism = 1
	}
	e.stageLog(fmt.Sprintf("开始提取 %d 个 .arc 文件（并发 %d 线程）...", e.arcTotal, maxParallelism))
	e.beginProgressBar()
	e.processAllParallel(arcFiles, maxParallelism)
	e.endProgressBar()

	// ---------- 5. 汇总 ----------
	e.successLog("提取完成")
	e.infoLog(fmt.Sprintf("  已处理 .arc 文件：%d 个", atomic.LoadInt64(&e.arcProcessed)))
	e.infoLog(fmt.Sprintf("  成功提取文件    ：%d 个", atomic.LoadInt64(&e.extractedCount)))
	if converted := atomic.LoadInt64(&e.convertedCount); converted > 0 {
		e.infoLog(fmt.Sprintf("  转换完成        ：%d 个（.nei→.csv / 图像→.png / 音频提取）", converted))
	}
	e.infoLog(fmt.Sprintf("  保存位置        ：%s", e.fullExportDir))
	e.infoLog(fmt.Sprintf("  耗时            ：%.1f 秒", time.Since(start).Seconds()))
	return true
}

// ==================== 核心处理 ====================

// searchArcFiles 递归搜索工作目录下的所有 .arc 文件。
func (e *Extractor) searchArcFiles() []string {
	var result []string
	found := 0
	err := filepath.WalkDir(e.workingDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过不可访问的目录/文件，整体结果仍会走 err 汇总
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(p), ".arc") {
			result = append(result, p)
			found++
			if found%10 == 0 && e.useCursor {
				e.safeStatusLine(fmt.Sprintf("→ 正在搜索 .arc 文件... 已找到 %d 个", found))
			}
		}
		return nil
	})
	if err != nil {
		e.errorLog(fmt.Sprintf("搜索 .arc 文件时出错：%v", err))
	}
	e.safeStatusLine(fmt.Sprintf("✓ 扫描完成：共找到 %d 个 .arc 文件", found))
	fmt.Println()
	return result
}

// processArc 读取并提取单个 .arc 文件中的内容。
func (e *Extractor) processArc(arcPath string) {
	arcRel, err := filepath.Rel(e.workingDir, arcPath)
	if err != nil {
		arcRel = arcPath
	}

	f, err := os.Open(arcPath)
	if err != nil {
		e.errorLog(fmt.Sprintf("无法读取 .arc 文件（可能已损坏或不是合法的 arc 文件）：%s", arcRel))
		return
	}
	defer f.Close()

	// 解析 ARC 元数据；文件载荷由 f 延迟支持，因此处理期间保持 f 打开
	afs, err := arc.ReadArc(f)
	if err != nil {
		e.errorLog(fmt.Sprintf("无法读取 .arc 文件（可能已损坏或不是合法的 arc 文件）：%s", arcRel))
		return
	}

	var arcExtracted int64
	for _, entry := range arc.AllFiles(afs) {
		innerRel := entry.RelativePath()
		if len(innerRel) == 0 {
			continue
		}

		ext := strings.ToLower(filepath.Ext(entry.Name))
		// 目标目录 = 导出目录 + .arc 相对路径（含 .arc 名字）+ 包内目录
		dir := filepath.Join(e.fullExportDir, arcRel, filepath.Dir(innerRel))
		targetPath := filepath.Join(dir, filepath.Base(innerRel))
		// 待定条目的中间文件：与目标同一目录，后缀 .arc.tmp
		tmpPath := targetPath + ".arc.tmp"

		processed := false
		switch e.decide(ext) {
		case actSkip:
			continue
		case actKeepRaw:
			if e.extractEntryTo(entry, targetPath) {
				processed = true
				e.convertIfNeeded(targetPath) // .nei → .csv
			}
		case actConvertImage:
			processed = e.handleConvertImage(entry, tmpPath, targetPath)
		case actDetect:
			processed = e.handleDetect(entry, tmpPath, targetPath, ext)
		}
		if processed {
			arcExtracted++
		}
	}

	atomic.AddInt64(&e.extractedCount, arcExtracted)
	if arcExtracted > 0 {
		e.infoLog(fmt.Sprintf("完成：%s，提取 %d 个文件", arcRel, arcExtracted))
	}
}

// ==================== 类型判定与动作调度 ====================

// entryAction 表示一条 arc 条目最终的处理动作。
type entryAction int

const (
	actSkip         entryAction = iota // 跳过：不落盘、不转换
	actKeepRaw                         // 原样保留
	actConvertImage                    // 转为 .png（只留 .png，原始数据不落盘）
	actDetect                          // 后缀不明确：写到目标目录 .arc.tmp 后内容检测，再决定
)

// 已知格式后缀集合：后缀能直接确定格式，无需内容检测。
var audioRawExtSet = map[string]struct{}{
	".ogg": {}, ".wav": {}, ".fsb": {}, ".fsb5": {}, ".audio": {},
}

var imageRawExtSet = map[string]struct{}{
	".png": {}, ".jpg": {}, ".jpeg": {}, ".bmp": {}, ".gif": {}, ".webp": {}, ".tga": {}, ".dds": {},
}

// 后缀不明确、需要内容检测的集合（内容可能是音频/图像，也可能不是）。
var ambiguousExtSet = map[string]struct{}{
	".bytes": {}, ".asset": {}, ".audioclip": {},
}

func isAudioRawExt(ext string) bool { _, ok := audioRawExtSet[ext]; return ok }
func isImageRawExt(ext string) bool { _, ok := imageRawExtSet[ext]; return ok }
func isAmbiguousExt(ext string) bool {
	if ext == "" {
		return true
	}
	_, ok := ambiguousExtSet[ext]
	return ok
}

// wantAudio / wantImage：勾选了对应选项或“任意类型”时才对音频/图像做识别与转换。
func (e *Extractor) wantAudio() bool { return e.extractAll || e.hasToken("audio") }
func (e *Extractor) wantImage() bool { return e.extractAll || e.hasToken("image") }

func (e *Extractor) hasToken(t string) bool {
	_, ok := e.extensions[t]
	return ok
}

// hasExtension 以忽略大小写的方式判断扩展名是否在已选集合中（.ks/.nei 等后缀选项）。
func (e *Extractor) hasExtension(ext string) bool {
	if _, ok := e.extensions[ext]; ok {
		return true
	}
	for k := range e.extensions {
		if strings.EqualFold(k, ext) {
			return true
		}
	}
	return false
}

// decide 依据“后缀优先、内容兜底”策略为条目分派动作。
// 参考 build/external/MeidoSerialization 源码结论：
//   - IsKCESNative* 只识别 KCES “MSKCESO1 魔数 + ClassID”的 Unity 独立对象，
//     对 arc 里真正的 .ogg/.tex/.png 文件必然返回 false，因此这些后缀不做检测；
//   - 只有 .bytes/.asset/无后缀 等可疑条目才需要内容检测。
func (e *Extractor) decide(ext string) entryAction {
	switch {
	case isAudioRawExt(ext): // 原始音频（.ogg/.wav/.fsb/...）：后缀即格式
		if e.wantAudio() {
			return actKeepRaw
		}
		return actSkip
	case ext == ".tex": // COM3D2 TEX：后缀即格式，直接走图像转换
		if e.wantImage() {
			return actConvertImage
		}
		return actSkip
	case isImageRawExt(ext): // 已是通用图像（.png/.jpg/...）：后缀即格式
		if e.wantImage() {
			return actKeepRaw
		}
		return actSkip
	case isAmbiguousExt(ext): // .bytes/.asset/无后缀：内容检测
		if e.wantAudio() || e.wantImage() {
			return actDetect
		}
		if e.hasExtension(ext) {
			return actKeepRaw
		}
		return actSkip
	default: // 其它有明确后缀的条目（.ks/.nei 等）
		if e.extractAll || e.hasExtension(ext) {
			return actKeepRaw
		}
		return actSkip
	}
}

// ==================== 多线程并行提取 ====================

// processAllParallel 通过 goroutine 并行处理所有 .arc 文件：
// 每个 .arc 提交一个任务，用信号量把并发数限制在 CPU 核心数附近，
// 兼顾 CPU 利用率与 IO 等待。
func (e *Extractor) processAllParallel(arcFiles []string, maxParallelism int) {
	sem := make(chan struct{}, maxParallelism)
	var wg sync.WaitGroup
	for _, path := range arcFiles {
		path := path
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() {
				<-sem
				atomic.AddInt64(&e.arcProcessed, 1)
				e.refreshProgressBar()
			}()
			// 单个 .arc 的意外 Panic 不能拖垮其它并行任务
			defer func() {
				if r := recover(); r != nil {
					rel, _ := filepath.Rel(e.workingDir, path)
					e.errorLog(fmt.Sprintf("处理 .arc 文件时发生未捕获异常：%s\n原因：%v", rel, r))
				}
			}()
			e.processArc(path)
		}()
	}
	wg.Wait()
}

// ==================== .nei 自动转换 ====================

// convertIfNeeded 提取成功后把 .nei 转成同目录同名 .csv（等价于原版
// convert2csv 子进程）。.tex 的“转 .png”已由 actConvertImage / actDetect
// 的内容转换流程取代。失败仅记录警告，不影响提取结果。
func (e *Extractor) convertIfNeeded(targetPath string) {
	if !strings.EqualFold(filepath.Ext(targetPath), ".nei") {
		return
	}
	svc := &COM3D2Service.NeiService{}
	outputPath := strings.TrimSuffix(targetPath, filepath.Ext(targetPath)) + ".csv"
	if err := svc.NeiFileToCSVFile(targetPath, outputPath); err != nil {
		e.warnLog(fmt.Sprintf("转换失败：%s\n%v", targetPath, err))
		return
	}
	atomic.AddInt64(&e.convertedCount, 1)
}

// ==================== 图像的识别与转换 ====================

// isImageFileContent 内容识别：KCES 原生 Texture2D/Sprite，或 COM3D2 TEX 签名。
func isImageFileContent(p string) bool {
	if KCESService.IsKCESNativeTexture2DFile(p) || KCESService.IsKCESNativeSpriteFile(p) {
		return true
	}
	info, matched, err := (&COM3D2Service.CommonService{}).TryFileTypeDetermine(p)
	return err == nil && matched && info.FileType == "tex"
}

// convertTmpToPng 把已知图像内容（KCES Texture2D/Sprite 或 COM3D2 TEX）转为 .png。
// 输入是 .arc.tmp 中间文件（后缀不是 .tex），不能按后缀走 ConvertAnyToAnyAndWrite，
// 因此按内容直接调用各类型的转换方法，输出路径显式指定。
func convertTmpToPng(tmpPath, outPng string) error {
	ctx := context.Background()
	svc := &KCESService.NativeUnityMediaService{}
	if KCESService.IsKCESNativeTexture2DFile(tmpPath) {
		return svc.ConvertTexture2DToImage(ctx, tmpPath, outPng, "png", application.DefaultMaxOutputBytes)
	}
	if KCESService.IsKCESNativeSpriteFile(tmpPath) {
		return svc.ConvertSpriteToPNG(ctx, tmpPath, outPng, application.DefaultMaxOutputBytes)
	}
	tex, err := (&COM3D2Service.TexService{}).ReadTexFile(tmpPath)
	if err != nil {
		return err
	}
	return COM3D2.ConvertTexToImageAndWrite(tex, outPng, false)
}

// ==================== 单条目处理 ====================

// extractEntryTo 把 .arc 内的一个文件原样写出到磁盘（含覆盖检查与目录创建）。
func (e *Extractor) extractEntryTo(entry *arc.File, targetPath string) bool {
	if !e.checkNotExists(targetPath) {
		return false
	}
	// File.Extract 内部：读取数据（压缩时自动解压）+ 创建目录 + 写文件
	if err := entry.Extract(targetPath); err != nil {
		e.errorLog(fmt.Sprintf("提取文件失败：%s\n原因：%v", targetPath, err))
		return false
	}
	return true
}

// writeTmp 把 arc 条目解压写到目标目录下的 .arc.tmp 中间文件。
// 内容判断函数（IsKCESNative* / TryFileTypeDetermine）只能读磁盘路径，
// 因此待定条目先落盘到目标目录，决定后再重命名保留 / 转换 / 删除。
func (e *Extractor) writeTmp(entry *arc.File, tmpPath string) bool {
	if err := entry.Extract(tmpPath); err != nil {
		e.errorLog(fmt.Sprintf("提取文件失败：%s\n原因：%v", tmpPath, err))
		return false
	}
	return true
}

// checkNotExists 未勾选覆盖时，目标已存在则静默跳过并返回 false。
func (e *Extractor) checkNotExists(target string) bool {
	if !e.overwrite {
		if _, err := os.Stat(target); err == nil {
			return false
		}
	}
	return true
}

// handleConvertImage 处理“图像”动作（.tex 后缀条目）：条目 → .png（只留 .png）。
func (e *Extractor) handleConvertImage(entry *arc.File, tmpPath, targetPath string) bool {
	outPng := baseWithNewExt(targetPath, ".png")
	if !e.checkNotExists(outPng) {
		return false
	}
	if !e.writeTmp(entry, tmpPath) {
		return false
	}
	defer os.Remove(tmpPath) // 最终只保留转换产物

	if err := convertTmpToPng(tmpPath, outPng); err != nil {
		e.warnLog(fmt.Sprintf("转换失败：%s\n%v", targetPath, err))
		return false
	}
	atomic.AddInt64(&e.convertedCount, 1)
	return true
}

// handleDetect 处理“内容检测”动作：先写到目标目录的 .arc.tmp，
// 依次判断音频（KCES AudioClip）、图像（KCES Texture2D/Sprite、COM3D2 TEX），
// 命中则转换；都不命中则按“任意类型/已选后缀”决定原样保留或跳过。
func (e *Extractor) handleDetect(entry *arc.File, tmpPath, targetPath, ext string) bool {
	if !e.writeTmp(entry, tmpPath) {
		return false
	}
	defer os.Remove(tmpPath)

	// 1) 音频：KCES 原生 AudioClip（内容识别，扩展名按载荷真实签名）
	if e.wantAudio() && KCESService.IsKCESNativeAudioClipFile(tmpPath) {
		svc := &KCESService.NativeUnityMediaService{}
		outExt, err := svc.DetectAudioClipExtension(context.Background(), tmpPath)
		if err != nil {
			e.warnLog(fmt.Sprintf("音频检测失败：%s\n%v", targetPath, err))
			return false
		}
		if outExt == "" {
			outExt = ".audio"
		}
		out := baseWithNewExt(targetPath, outExt)
		if !e.checkNotExists(out) {
			return false
		}
		if err := svc.ExtractAudioClip(context.Background(), tmpPath, out, application.DefaultMaxOutputBytes); err != nil {
			e.warnLog(fmt.Sprintf("音频提取失败：%s\n%v", targetPath, err))
			return false
		}
		atomic.AddInt64(&e.convertedCount, 1)
		return true
	}

	// 2) 图像：KCES Texture2D/Sprite 或 COM3D2 TEX（内容识别）→ .png
	if e.wantImage() && isImageFileContent(tmpPath) {
		outPng := baseWithNewExt(targetPath, ".png")
		if !e.checkNotExists(outPng) {
			return false
		}
		if err := convertTmpToPng(tmpPath, outPng); err != nil {
			e.warnLog(fmt.Sprintf("转换失败：%s\n%v", targetPath, err))
			return false
		}
		atomic.AddInt64(&e.convertedCount, 1)
		return true
	}

	// 3) 不是音频也不是图像：任意类型或已选后缀命中才原样保留，否则跳过
	if e.extractAll || e.hasExtension(ext) {
		if !e.checkNotExists(targetPath) {
			return false
		}
		if err := os.Rename(tmpPath, targetPath); err != nil {
			e.errorLog(fmt.Sprintf("保留文件失败：%s\n原因：%v", targetPath, err))
			return false
		}
		return true
	}
	// 都不满足：删除 .arc.tmp，不落任何文件
	return false
}

// baseWithNewExt 返回把 targetPath 的扩展名替换为 newExt 的路径（newExt 含点号）。
func baseWithNewExt(targetPath, newExt string) string {
	base := filepath.Base(targetPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	return filepath.Join(filepath.Dir(targetPath), stem+newExt)
}

// ==================== 进度条 ====================

// beginProgressBar 开始显示底部进度条。
func (e *Extractor) beginProgressBar() {
	if !e.useCursor {
		return
	}
	e.consoleMu.Lock()
	defer e.consoleMu.Unlock()
	e.progressActive = true
	e.refreshProgressBarLocked()
}

// refreshProgressBar 在控制台最底部重绘进度条（多线程下由控制台锁串行化）。
func (e *Extractor) refreshProgressBar() {
	if !e.progressActive || !e.useCursor {
		return
	}
	e.consoleMu.Lock()
	defer e.consoleMu.Unlock()
	e.refreshProgressBarLocked()
}

func (e *Extractor) refreshProgressBarLocked() {
	width, height := windowSize()
	row := height - 1
	prefix, bar, suffix := e.buildProgressText(width)

	fmt.Print(moveTo(row, 0))
	fmt.Print(ansiCyan + prefix)
	fmt.Print(ansiGreen + bar)
	fmt.Print(ansiCyan + suffix)
	fmt.Print(ansiReset)
	// 按显示宽度补白（全角中文占 2 列），避免字符补白超宽触发换行使进度条跳动
	displayLength := displayWidth(prefix) + len(bar) + displayWidth(suffix)
	pad := width - displayLength - 1
	if pad > 0 {
		fmt.Print(strings.Repeat(" ", pad))
	}
}

// buildProgressText 构造进度条文本段：前缀 + 进度条 + 后缀。
func (e *Extractor) buildProgressText(width int) (prefix, bar, suffix string) {
	processed := atomic.LoadInt64(&e.arcProcessed)
	total := atomic.LoadInt64(&e.arcTotal)
	extracted := atomic.LoadInt64(&e.extractedCount)

	var pct int
	if total > 0 {
		pct = int((float64(processed)*100.0/float64(total))+0.5) + 0
	}
	barWidth := width - 45
	if barWidth < 10 {
		barWidth = 10
	}
	if barWidth > 60 {
		barWidth = 60
	}
	filled := 0
	if total > 0 {
		f := int((float64(processed)*float64(barWidth)/float64(total))+0.5) + 0
		if f > barWidth {
			f = barWidth
		}
		filled = f
	}
	bar = strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	prefix = fmt.Sprintf("进度 %d%% ", pct)
	suffix = fmt.Sprintf("  处理 %d/%d  已提取 %d 个文件", processed, total, extracted)
	return
}

// endProgressBar 结束进度条：清空进度行并把光标复位。
func (e *Extractor) endProgressBar() {
	if !e.progressActive || !e.useCursor {
		return
	}
	e.consoleMu.Lock()
	defer e.consoleMu.Unlock()
	width, height := windowSize()
	row := height - 1
	fmt.Print(moveTo(row, 0))
	fmt.Print(strings.Repeat(" ", width))
	fmt.Print(moveTo(row, 0))
	e.progressActive = false
}

// ==================== 日志 ====================

// log 统一的日志输出入口。进度条激活时，先清空最底部进度行用于输出日志，
// 日志写完后再把进度条画回最底部，保证进度条始终可见。
func (e *Extractor) log(levelName, color, message string) {
	prefix := ""
	switch levelName {
	case "警告":
		prefix = "⚠ "
	case "错误", "致命":
		prefix = "✗ "
	case "调试":
		prefix = "[调试] "
	case "跟踪":
		prefix = "[跟踪] "
	case "阶段":
		prefix = "→ "
	case "成功":
		prefix = "✓ "
	}
	line := prefix + message

	e.consoleMu.Lock()
	defer e.consoleMu.Unlock()

	if e.progressActive && e.useCursor {
		width, height := windowSize()
		row := height - 1
		fmt.Print(moveTo(row, 0))
		fmt.Print(strings.Repeat(" ", width))
		fmt.Print(moveTo(row, 0))
		fmt.Print(color, line, "\n", ansiReset)
		e.refreshProgressBarLocked()
		return
	}

	fmt.Print(color, line, "\n", ansiReset)
}

func (e *Extractor) stageLog(message string)   { e.log("阶段", ansiCyan, message) }
func (e *Extractor) infoLog(message string)    { e.log("信息", ansiWhite, message) }
func (e *Extractor) successLog(message string) { e.log("成功", ansiGreen, message) }
func (e *Extractor) warnLog(message string)    { e.log("警告", ansiYellow, message) }
func (e *Extractor) errorLog(message string)   { e.log("错误", ansiRed, message) }

// safeStatusLine 用于搜索阶段的状态行：交互控制台用 \r 原位刷新不换行；
// 输出被重定向/捕获时退化为普通单行输出。
func (e *Extractor) safeStatusLine(text string) {
	if !e.useCursor {
		fmt.Println(text)
		return
	}
	width, _ := windowSize()
	fmt.Print("\r" + text)
	// 用显示宽度计算补白，防止字符补白超出窗口宽度触发换行
	pad := width - displayWidth(text) - 1
	if pad > 0 {
		fmt.Print(strings.Repeat(" ", pad))
	}
}
