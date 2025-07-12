package main

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

type ManifestItem struct {
	ID        string `xml:"id,attr"`
	Href      string `xml:"href,attr"`
	MediaType string `xml:"media-type,attr"`
}

type Manifest struct {
	Items []ManifestItem `xml:"item"`
}

type Spine struct {
	Itemrefs []struct {
		IDRef string `xml:"idref,attr"`
	} `xml:"itemref"`
}

type Package struct {
	Manifest Manifest `xml:"manifest"`
	Spine    Spine    `xml:"spine"`
}

func findOpfFile(r *zip.ReadCloser) (string, error) {
	for _, f := range r.File {
		if f.Name == "META-INF/container.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()
			type Rootfile struct {
				FullPath string `xml:"full-path,attr"`
			}
			type Container struct {
				Rootfiles struct {
					Rootfile Rootfile `xml:"rootfile"`
				} `xml:"rootfiles"`
			}
			var c Container
			if err := xml.NewDecoder(rc).Decode(&c); err != nil {
				return "", err
			}
			return c.Rootfiles.Rootfile.FullPath, nil
		}
	}
	return "", fmt.Errorf("container.xml not found")
}

func getImagesByManifestOrder(opfData []byte, opfDir string) ([]string, error) {
	var pkg Package
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return nil, err
	}
	var imgHrefs []string
	for _, item := range pkg.Manifest.Items {
		if strings.HasPrefix(item.MediaType, "image/") {
			imgHrefs = append(imgHrefs, filepath.Clean(filepath.Join(opfDir, item.Href)))
		}
	}
	return imgHrefs, nil
}

func getImagesByPageHtml(opfData []byte, r *zip.ReadCloser, opfDir string) ([]string, error) {
	var pkg Package
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return nil, err
	}
	manifestMap := make(map[string]ManifestItem)
	for _, item := range pkg.Manifest.Items {
		manifestMap[item.ID] = item
	}
	var pageHtmlItems []ManifestItem
	for _, ref := range pkg.Spine.Itemrefs {
		item, ok := manifestMap[ref.IDRef]
		if ok && item.MediaType == "application/xhtml+xml" {
			pageHtmlItems = append(pageHtmlItems, item)
		}
	}
	fileMap := make(map[string]*zip.File)
	for _, f := range r.File {
		fileMap[f.Name] = f
	}
	imgSrcPattern := regexp.MustCompile(`<img\s+[^>]*src\s*=\s*['"]([^'"]+)['"][^>]*\/?>`)
	var imgHrefs []string
	seen := make(map[string]struct{})
	for _, page := range pageHtmlItems {
		pagePath := filepath.Clean(filepath.Join(opfDir, page.Href))
		pageFile, ok := fileMap[pagePath]
		if !ok {
			continue
		}
		rc, err := pageFile.Open()
		if err != nil {
			continue
		}
		htmlData, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		matches := imgSrcPattern.FindAllSubmatch(htmlData, -1)
		for _, m := range matches {
			src := string(m[1])
			imgPath := filepath.Clean(filepath.Join(filepath.Dir(pagePath), src))
			if _, ok := fileMap[imgPath]; !ok {
				continue
			}
			if _, ok := seen[imgPath]; !ok {
				imgHrefs = append(imgHrefs, imgPath)
				seen[imgPath] = struct{}{}
			}
		}
	}
	return imgHrefs, nil
}

func convertEpub(epubFile string, mode string, progress func(p float64, text string)) error {
	r, err := zip.OpenReader(epubFile)
	if err != nil {
		return fmt.Errorf("open epub: %w", err)
	}
	defer r.Close()

	opfPath, err := findOpfFile(r)
	if err != nil {
		return fmt.Errorf("find opf: %w", err)
	}

	var opfData []byte
	for _, f := range r.File {
		if f.Name == opfPath {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			opfData, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return err
			}
			break
		}
	}
	if opfData == nil {
		return fmt.Errorf("opf file not found")
	}
	opfDir := filepath.Dir(opfPath)
	var imgOrder []string
	switch mode {
	case "manifest":
		imgOrder, err = getImagesByManifestOrder(opfData, opfDir)
	case "page":
		imgOrder, err = getImagesByPageHtml(opfData, r, opfDir)
	default:
		return fmt.Errorf("unknown mode")
	}
	if err != nil {
		return fmt.Errorf("parse images: %w", err)
	}

	imgMap := make(map[string]*zip.File)
	for _, f := range r.File {
		imgMap[f.Name] = f
	}
	outZip := epubFile[:len(epubFile)-len(filepath.Ext(epubFile))] + ".zip"
	zf, err := os.Create(outZip)
	if err != nil {
		return err
	}
	defer zf.Close()
	zw := zip.NewWriter(zf)
	defer zw.Close()

	total := float64(len(imgOrder))
	for i, imgPath := range imgOrder {
		imgFile, ok := imgMap[imgPath]
		if !ok {
			progress(float64(i)/total, fmt.Sprintf("图片缺失: %s", imgPath))
			continue
		}
		rc, err := imgFile.Open()
		if err != nil {
			progress(float64(i)/total, fmt.Sprintf("打开失败: %s", imgPath))
			continue
		}
		ext := strings.ToLower(filepath.Ext(imgFile.Name))
		imgName := fmt.Sprintf("%d%s", i, ext)
		w, err := zw.Create(imgName)
		if err != nil {
			rc.Close()
			progress(float64(i)/total, fmt.Sprintf("压缩失败: %s", imgName))
			continue
		}
		_, copyErr := io.Copy(w, rc)
		rc.Close()
		if copyErr != nil {
			progress(float64(i)/total, fmt.Sprintf("写入失败: %s", imgName))
			continue
		}
		progress(float64(i+1)/total, fmt.Sprintf("已导出 %d/%d", i+1, int(total)))
	}
	progress(1, "完成！")
	return nil
}

func scanEpubsInDir(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	files := []string{}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(strings.ToLower(entry.Name()), ".epub") {
			files = append(files, filepath.Join(root, entry.Name()))
		}
	}
	return files, nil
}

func main() {
	a := app.New()
	w := a.NewWindow("EPUB图片批量提取导出")
	w.Resize(fyne.NewSize(500, 500))
	fileList := widget.NewLabel("未选择")
	var filePaths []string
	var selectedDir string
	modeRadio := widget.NewRadioGroup([]string{"按页面顺序(page)", "按manifest顺序(manifest)"}, nil)
	modeRadio.SetSelected("按页面顺序(page)")
	convertBtn := widget.NewButton("开始转换", func() {})
	convertBtn.Disable()
	progressList := container.NewVBox()

	// 初始化时创建一个空容器
	rightContainer := container.NewMax()

	// 刷新按钮
	refreshBtn := widget.NewButton("刷新文件列表", func() {
		if selectedDir == "" {
			dialog.ShowInformation("请先选择目录", "未选择有效目录，请先点击“选择目录”", w)
			return
		}
		eps, err := scanEpubsInDir(selectedDir)
		if err != nil {
			dialog.ShowError(fmt.Errorf("扫描目录失败: %v", err), w)
			return
		}
		if len(eps) == 0 {
			fileList.SetText("该目录下未发现epub文件")
			convertBtn.Disable()
			return
		}
		filePaths = eps
		fileList.SetText(fmt.Sprintf("已发现 %d 个epub文件:\n%s", len(filePaths), strings.Join(filePaths, "\n")))
		convertBtn.Enable()
	})

	selectBtn := widget.NewButton("选择包含epub文件的目录", func() {
		dialog.ShowFolderOpen(func(u fyne.ListableURI, err error) {
			if err != nil || u == nil {
				return
			}
			dirPath := u.Path()
			selectedDir = dirPath
			eps, err := scanEpubsInDir(dirPath)
			if err != nil {
				dialog.ShowError(fmt.Errorf("扫描目录失败: %v", err), w)
				return
			}
			if len(eps) == 0 {
				fileList.SetText("该目录下未发现epub文件")
				convertBtn.Disable()
				return
			}
			filePaths = eps
			fileList.SetText(fmt.Sprintf("已发现 %d 个epub文件:\n%s", len(filePaths), strings.Join(filePaths, "\n")))
			convertBtn.Enable()
		}, w)
	})

	convertBtn.OnTapped = func() {
		convertBtn.Disable()
		progressList.Objects = nil
		var wg sync.WaitGroup

		// 将进度列表设置到右侧容器中并展示
		rightContainer.Objects = []fyne.CanvasObject{container.NewVScroll(progressList)}
		rightContainer.Refresh()

		for idx, file := range filePaths {
			name := filepath.Base(file)
			progBar := widget.NewProgressBar()
			progBar.Resize(fyne.NewSize(700, 30))
			label := widget.NewLabel("")
			label.Wrapping = fyne.TextWrapWord
			row := container.NewVBox(widget.NewLabel(name), progBar, label)
			progressList.Add(row)
			progressList.Refresh()
			wg.Add(1)
			go func(idx int, file string, pb *widget.ProgressBar, label *widget.Label) {
				defer wg.Done()
				var mode string
				if strings.Contains(modeRadio.Selected, "page") {
					mode = "page"
				} else {
					mode = "manifest"
				}
				err := convertEpub(file, mode, func(p float64, text string) {
					pb.SetValue(p)
					label.SetText(text)
				})
				if err != nil {
					label.SetText(fmt.Sprintf("错误: %v", err))
				}
			}(idx, file, progBar, label)
		}
		go func() {
			wg.Wait()
			convertBtn.Enable()
		}()
	}

	leftContent := container.NewVBox(
		widget.NewLabel("操作步骤："),
		widget.NewLabel("1. 选择一个包含epub的目录"),
		widget.NewLabel("2. 可随时点击“刷新文件列表”"),
		widget.NewLabel("3. 选择图片提取模式"),
		widget.NewLabel("4. 点击“开始转换”"),
		fileList,
		container.NewHBox(selectBtn, refreshBtn),
		modeRadio,
		convertBtn,
		widget.NewSeparator(),
	)

	// 使用布局管理器来调整显示逻辑
	content := container.New(layout.NewBorderLayout(nil, nil, leftContent, rightContainer), leftContent, rightContainer)
	w.SetContent(content)
	w.ShowAndRun()
}
