# COM3D2.ExportUtility

交互式控制台工具：递归扫描工作目录下所有 `.arc` 资源包，按选择的类型过滤并提取文件，音频、图像在提取时自动识别与转换。

本项目为 C# 版 ExportUtility 的 Go 重写版，不再调用 `MeidoSerialization.exe` 子进程，也不再依赖 CM3D2.ToolKit，而是把 [MeidoSerialization](https://github.com/MeidoPromotionAssociation/MeidoSerialization)（Go 模块，v2.3.0）作为库直接使用其源码能力。

## 功能特性

- 交互式界面：方向键移动、空格多选、回车确认，支持中文显示与输入（UTF-8 控制台代码页）。
- 递归搜索 `.arc` 文件，多线程并行提取，底部实时进度条。
- 可多选提取类型：

  | 选项 | 行为 |
  | --- | --- |
  | 任意类型 | 提取包内全部文件；其中的音频、图像会被自动识别并转换 |
  | .ks | 按后缀匹配，原样提取 |
  | .nei | 按后缀匹配，原样提取并自动转出同名 `.csv` |
  | 音频（.ogg） | 识别原始音频（`.ogg/.wav/.fsb/.fsb5`）与 KCES AudioClip（内容识别）；AudioClip 提取其内嵌音频载荷，扩展名按载荷真实签名命名（`.ogg/.wav/.fsb/.audio`，不转码） |
  | 图像（.tex → .png） | 识别 COM3D2 `.tex` 与 KCES Texture2D/Sprite（内容识别），转换为 `.png`（只保留 `.png`）；`.png/.jpg/.jpeg/.bmp/.gif/.webp` 等已是图像的按原样保留 |

- 内容识别而非纯后缀匹配：
  - `.ogg/.tex/.png` 等后缀能直接确定格式的文件不做多余检测；
  - 后缀不明确的条目（`.bytes/.asset/.audioclip`、无后缀）先写入目标目录同名 `.arc.tmp`，再调用 MeidoSerialization 的判断函数识别（`IsKCESNativeAudioClipFile` / `IsKCESNativeTexture2DFile` / `IsKCESNativeSpriteFile` / TEX 签名探测），命中则转换，未命中则按“任意类型/已选后缀”决定原样保留或跳过。
  - 名为 `.tex`/`.ogg` 但内容不符的条目会被跳过，不留任何文件。

## 目录结构

```
COM3D2.ExportUtility/
├── main.go             交互式界面入口（对应 C# Program.cs）
├── extractor.go        提取引擎（对应 C# ExportUtility.cs）
├── conio.go            控制台能力抽象（按键/光标/显示宽度）
├── conio_windows.go    Windows 控制台实现（kernel32 原始按键与 ANSI 输出）
├── conio_other.go      非 Windows 降级实现
├── go.mod / go.sum     依赖：github.com/MeidoPromotionAssociation/MeidoSerialization/v2（远程模块）
```

## 环境要求

- Go 1.27 及以上；若本机为旧版本，可设置 `GOTOOLCHAIN=auto` 让 Go 自动下载对应工具链（已验证 go 1.26.8 + auto 可正常构建）。
- 图像（TEX→PNG）转换依赖 ImageMagick 7（需要 `magick` 命令可用）；未安装时该转换会失败并仅记录警告，不影响提取。
- KCES 纹理/精灵转 PNG、AudioClip 音频提取、`.nei→.csv` 均为纯 Go 实现，无需外部程序。
- 建议在 Windows 控制台（PowerShell / cmd / Windows Terminal）中运行。

## 构建

```powershell
$env:GOTOOLCHAIN="auto"      # 旧版 Go 自动切换到 go1.27 工具链
go build -o COM3D2ExportUtility.exe .
```

## 使用

1. 把 `COM3D2ExportUtility.exe` 放到包含 `.arc` 文件的目录（例如游戏安装目录）后运行；也可放到任意目录，文件导出路径用绝对路径时不受工作目录限制。
2. 按界面提示操作：
   - 类型多选列表：`↑/↓` 移动，`空格` 勾选/取消，`回车` 确认（可多选）。
   - 文件导出到：默认 `ExportUtilityResources`（相对工作目录），可直接回车；支持 `←/→` 移动光标、退格/删除编辑。
   - 覆盖旧文件：`空格` 切换，`回车` 确认。
   - 取消 / 开始：`↑/↓` 切换，`回车` 确认。
3. 导出结构：`导出目录/<arc 文件名>/<包内相对路径>`。

## 行为说明

- 待定条目会先解压为目标目录下 `<原文件名>.arc.tmp` 中间文件，再决定保留（重命名）、转换（完成后删除）或跳过（直接删除）；程序中途被强制终止可能残留 `.arc.tmp`，属正常现象。
- 未勾选“覆盖旧文件”且目标已存在时，该文件被静默跳过（不提示）。
- 勾选“覆盖旧文件”时，转换产物（`.png`/音频）会直接覆盖同名旧文件。
- 音频提取为无损提取：按 AudioClip 内联载荷的签名给出对应扩展名，不做音质转换。