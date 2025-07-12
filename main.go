package main

import (
	"archive/zip"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

func getImagesByManifestOrder(opfData []byte) ([]string, error) {
	// 按 manifest 顺序解析所有图片
	var pkg Package
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return nil, err
	}
	imgs := make(map[string]string)
	var imgIDs []string
	for _, item := range pkg.Manifest.Items {
		if strings.HasPrefix(item.MediaType, "image/") {
			imgs[item.ID] = item.Href
		}
	}
	for _, item := range pkg.Manifest.Items {
		if _, ok := imgs[item.ID]; ok {
			imgIDs = append(imgIDs, item.ID)
		}
	}
	ordered := []string{}
	for _, id := range imgIDs {
		ordered = append(ordered, imgs[id])
	}
	return ordered, nil
}

func getImagesByPageHtml(opfData []byte, r *zip.ReadCloser, opfDir string) ([]string, error) {
	// 解析spine，按页面顺序依次取html，再从html内取img src
	var pkg Package
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return nil, err
	}
	// id => ManifestItem
	manifestMap := make(map[string]ManifestItem)
	for _, item := range pkg.Manifest.Items {
		manifestMap[item.ID] = item
	}
	// spine顺序中的页面
	var pageHtmlItems []ManifestItem
	for _, ref := range pkg.Spine.Itemrefs {
		item, ok := manifestMap[ref.IDRef]
		if ok && item.MediaType == "application/xhtml+xml" {
			pageHtmlItems = append(pageHtmlItems, item)
		}
	}

	// EPub文件索引
	fileMap := make(map[string]*zip.File)
	for _, f := range r.File {
		fileMap[f.Name] = f
	}

	// 用正则找img标签和src，兼容自闭合与正常标签
	imgSrcPattern := regexp.MustCompile(`<img\s+[^>]*src\s*=\s*['"]([^'"]+)['"][^>]*\/?>`)
	var imgHrefs []string
	seen := make(map[string]struct{}) // 去重（常见于某些epub同文件多次引用）

	for _, page := range pageHtmlItems {
		pagePath := filepath.Clean(filepath.Join(opfDir, page.Href))
		pageFile, ok := fileMap[pagePath]
		if !ok {
			fmt.Fprintf(os.Stderr, "[Warn] page file not found: %s\n", pagePath)
			continue
		}
		rc, err := pageFile.Open()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Warn] cannot open page file: %s\n", pageFile.Name)
			continue
		}
		htmlData, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Warn] cannot read page file: %s\n", pageFile.Name)
			continue
		}
		matches := imgSrcPattern.FindAllSubmatch(htmlData, -1)
		for _, m := range matches {
			src := string(m[1])
			// 构造图片真实路径（相对于页面文件）
			imgPath := filepath.Clean(filepath.Join(filepath.Dir(pagePath), src))
			if _, ok := fileMap[imgPath]; !ok {
				fmt.Fprintf(os.Stderr, "[Warn] img %s not found in archive\n", imgPath)
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

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"用法: %s [-mode mode] input.epub output.zip\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(),
			"    -mode 可选值: manifest/page   (默认: page)\n")
	}
	mode := flag.String("mode", "page", "顺序模式: manifest 或 page")
	flag.Parse()
	if flag.NArg() < 2 {
		flag.Usage()
		os.Exit(1)
	}
	epubPath := flag.Arg(0)
	zipPath := flag.Arg(1)

	r, err := zip.OpenReader(epubPath)
	if err != nil {
		panic(err)
	}
	defer r.Close()

	opfPath, err := findOpfFile(r)
	if err != nil {
		panic(err)
	}

	var opfData []byte
	for _, f := range r.File {
		if f.Name == opfPath {
			rc, err := f.Open()
			if err != nil {
				panic(err)
			}
			opfData, err = io.ReadAll(rc)
			rc.Close()
			if err != nil {
				panic(err)
			}
			break
		}
	}
	if opfData == nil {
		panic("opf file not found")
	}

	opfDir := filepath.Dir(opfPath)
	var imageOrder []string

	switch *mode {
	case "manifest":
		imageOrder, err = getImagesByManifestOrder(opfData)
		if err != nil {
			panic(err)
		}
		// 路径转为相对opf的真实路径
		for i := range imageOrder {
			imageOrder[i] = filepath.Clean(filepath.Join(opfDir, imageOrder[i]))
		}
	case "page":
		imageOrder, err = getImagesByPageHtml(opfData, r, opfDir)
		if err != nil {
			panic(err)
		}
	default:
		fmt.Fprintf(os.Stderr, "未知mode: %s，仅可用 page 或 manifest\n", *mode)
		os.Exit(1)
	}

	// 构建epub内容map
	imgMap := make(map[string]*zip.File)
	for _, f := range r.File {
		imgMap[f.Name] = f
	}

	// 写到zip
	outZip, err := os.Create(zipPath)
	if err != nil {
		panic(err)
	}
	defer outZip.Close()
	zw := zip.NewWriter(outZip)
	defer zw.Close()

	for i, imgPath := range imageOrder {
		imgFile, ok := imgMap[imgPath]
		if !ok {
			fmt.Fprintf(os.Stderr, "[Warn] image not found: %s, skip\n", imgPath)
			continue
		}
		rc, err := imgFile.Open()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Warn] cannot open image %s: %v, skip.\n", imgFile.Name, err)
			continue
		}
		// 提取扩展名，防止多 . 或大小写问题
		ext := strings.ToLower(filepath.Ext(imgFile.Name))
		imgName := fmt.Sprintf("%d%s", i, ext)
		w, err := zw.Create(imgName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Warn] cannot create %s in zip: %v, skip.\n", imgName, err)
			rc.Close()
			continue
		}
		_, copyErr := io.Copy(w, rc)
		rc.Close()
		if copyErr != nil {
			fmt.Fprintf(os.Stderr, "[Warn] cannot copy %s: %v, skip.\n", imgName, copyErr)
			continue
		}
	}
	fmt.Printf("Done. Extracted %d images into %s with mode [%s]\n", len(imageOrder), zipPath, *mode)
}
