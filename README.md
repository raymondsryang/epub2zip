# Epub to ZIP

`Epub2Zip` 是一个从 EPUB 文件批量提取图片并转换为 ZIP 文件的 小工具。

## 功能
- 选择一个包含一个或多个 EPUB 文件的目录
- 支持按 页面顺序 或 图片顺序 提取图片
- 将提取的图片以 ZIP 格式导出

## 使用说明

1. 选择一个包含 EPUB 的目录。
2. 点击“刷新文件列表”按钮以加载目录中的 EPUB 文件。
3. 选择图片提取模式（按页面顺序或按 manifest 顺序）。
4. 点击“开始转换”以开始提取和转换操作。
5. 提取进度将显示在窗口右侧。

## 安装和编译

### 先决条件

- Go 1.17或更高版本
- Fyne 库

### 编译步骤

```bash
git clone https://github.com/你的用户名/epub-image-extractor.git
cd epub-image-extractor
go mod tidy
go build -o epub-image-extractor