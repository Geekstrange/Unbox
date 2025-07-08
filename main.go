package main

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// 支持格式映射表
var supportedFormats = map[string]func(string, string) error{
	".zip":    extractZip,
	".tar":    extractTar,
	".tar.gz": extractTarGz,
	".gz":     extractGzip,
	".bz2":    extractBzip2,
	".zst":    extractZstd,
	".rar":    extractRar,
	".7z":     extract7z,
}

// 命令行参数
var (
	recursive  = flag.Bool("r", false, "递归解压嵌套压缩包")
	list       = flag.Bool("l", false, "列出压缩包内容（自动递归预览嵌套压缩包）")
	deleteOrig = flag.Bool("o", false, "解压后删除源文件")
	version    = flag.Bool("version", false, "显示版本信息")
	help       = flag.Bool("h", false, "显示帮助信息")
	supported  = flag.Bool("s", false, "显示支持格式列表")
)

const (
	versionText = `
------------Unbox version 0.0.3------------
Copyright 2025 Geekstrange

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

        http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.`

	supportedFormatsText = `
7z
Z
arj
br
bz2
cab
cpio
crx
deb
dmg
epub
gem
gz
hdr
jar
lha
lrz
lz
lzh
lzma
msi
rar
rpm
tar
tar.Z
tar.bz2
tar.gz
tar.lrz
tar.lz
tar.lzh
tar.lzma
tar.xz
tar.zst
taz
tb2
tbz
tbz2
tgz
tlz
txz
xpi
xz
zip
zst
zstd`
)

// ====================== 渐变色输出函数 ======================
func isTerminal() bool {
	fi, _ := os.Stdout.Stat()
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func addGradient(text string, startRGB, endRGB [3]int) string {
	if !isTerminal() || text == "" {
		return text
	}

	var result strings.Builder
	chars := []rune(text)
	for i, char := range chars {
		ratio := float64(i) / float64(len(chars)-1)
		if len(chars) == 1 {
			ratio = 0
		}

		r := int(float64(startRGB[0]) + (float64(endRGB[0])-float64(startRGB[0]))*ratio)
		g := int(float64(startRGB[1]) + (float64(endRGB[1])-float64(startRGB[1]))*ratio)
		b := int(float64(startRGB[2]) + (float64(endRGB[2])-float64(startRGB[2]))*ratio)

		// 确保RGB值在0-255范围内
		if r < 0 {
			r = 0
		} else if r > 255 {
			r = 255
		}
		if g < 0 {
			g = 0
		} else if g > 255 {
			g = 255
		}
		if b < 0 {
			b = 0
		} else if b > 255 {
			b = 255
		}

		result.WriteString(fmt.Sprintf("\033[38;2;%d;%d;%dm%c", r, g, b, char))
	}
	return result.String() + "\033[0m" // 重置颜色
}

// ====================== 核心解压函数 ======================
func extractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if strings.Contains(f.Name, "..") {
			return fmt.Errorf("拒绝解压含非法路径的文件: %s", f.Name)
		}

		targetPath := filepath.Join(dest, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(targetPath, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}

		outFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		defer outFile.Close()

		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()

		if _, err := io.Copy(outFile, rc); err != nil {
			return err
		}
	}
	return nil
}

func extractTar(src, dest string) error {
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer file.Close()
	return extractTarStream(tar.NewReader(file), dest)
}

func extractTarGz(src, dest string) error {
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzReader.Close()

	return extractTarStream(tar.NewReader(gzReader), dest)
}

func extractGzip(src, dest string) error {
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzReader.Close()

	baseName := filepath.Base(src)
	targetName := strings.TrimSuffix(baseName, ".gz")
	targetPath := filepath.Join(dest, targetName)

	outFile, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, gzReader)
	return err
}

func extractBzip2(src, dest string) error {
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer file.Close()

	bzReader := bzip2.NewReader(file)
	baseName := filepath.Base(src)
	targetName := strings.TrimSuffix(baseName, ".bz2")
	targetPath := filepath.Join(dest, targetName)

	outFile, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	_, err = io.Copy(outFile, bzReader)
	return err
}

func extractRar(src, dest string) error {
	cmd := exec.Command("unrar", "x", "-o+", src, dest)
	return cmd.Run()
}

func extract7z(src, dest string) error {
	cmd := exec.Command("7z", "x", "-o"+dest, src)
	return cmd.Run()
}

func extractZstd(src, dest string) error {
	baseName := filepath.Base(src)
	targetName := strings.TrimSuffix(baseName, ".zst")
	targetPath := filepath.Join(dest, targetName)
	cmd := exec.Command("zstd", "-d", src, "-o", targetPath)
	return cmd.Run()
}

// ====================== 辅助函数 ======================
func extractTarStream(tr *tar.Reader, dest string) error {
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		targetPath := filepath.Join(dest, header.Name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			outFile, err := os.Create(targetPath)
			if err != nil {
				return err
			}
			defer outFile.Close()

			if _, err := io.Copy(outFile, tr); err != nil {
				return err
			}
		}
	}
	return nil
}

func detectFormat(filename string) (string, error) {
	ext := filepath.Ext(filename)
	if _, exists := supportedFormats[ext]; exists {
		return ext, nil
	}

	if secondExt := filepath.Ext(strings.TrimSuffix(filename, ext)); secondExt != "" {
		compoundExt := secondExt + ext
		if _, exists := supportedFormats[compoundExt]; exists {
			return compoundExt, nil
		}
	}

	return "", fmt.Errorf("unsupported format: %s", ext)
}

// 递归解压实现
func recursiveUnpackDir(dir string, depth int) error {
	// 防止压缩炸弹
	if depth > 100 {
		return fmt.Errorf("疑似压缩炸弹: %s", dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	// 处理当前目录的压缩文件
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		fpath := filepath.Join(dir, entry.Name())
		ext, err := detectFormat(fpath)
		if err != nil {
			continue
		}

		fmt.Printf("解压嵌套包: %s\n", fpath)
		if err := supportedFormats[ext](fpath, dir); err != nil {
			log.Printf("解压失败: %v", err)
			continue
		}

		if err := os.Remove(fpath); err == nil {
			fmt.Printf("已删除: %s\n", fpath)
		}

		// 递归处理当前目录
		if err := recursiveUnpackDir(dir, depth+1); err != nil {
			return err
		}
	}

	// 处理子目录
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		subdir := filepath.Join(dir, entry.Name())
		if err := recursiveUnpackDir(subdir, depth+1); err != nil {
			return err
		}
	}

	return nil
}

// ====================== 预览功能（支持独立递归） ======================
func listContents(filename string, depth int) error {
	if depth > 10 { // 深度保护
		return fmt.Errorf("递归深度超过10层: %s", filename)
	}

	ext, err := detectFormat(filename)
	if err != nil {
		return err
	}

	// 根据扩展名调用预览函数
	switch ext {
	case ".zip":
		return listZipContents(filename, depth)
	case ".tar", ".tar.gz", ".tar.bz2", ".tar.xz":
		return listTarContents(filename, depth, ext == ".tar.gz", ext == ".tar.bz2", ext == ".tar.xz")
	case ".gz", ".bz2", ".xz", ".zst", ".rar", ".7z":
		return listSingleFileContents(filename, depth)
	default:
		return fmt.Errorf("预览不支持此格式: %s", ext)
	}
}

func listSingleFileContents(filename string, depth int) error {
	fileInfo, err := os.Stat(filename)
	if err != nil {
		return err
	}

	indent := strings.Repeat("  ", depth)
	fmt.Printf("%s%-40s %10d bytes\n", indent, filepath.Base(filename), fileInfo.Size())
	return nil
}

// ====================== 预览功能修复（支持嵌套压缩包内存预览） ======================
func listZipContents(filename string, depth int) error {
    r, err := zip.OpenReader(filename)
    if err != nil {
        return err
    }
    defer r.Close()

    indent := strings.Repeat("  ", depth)
    fmt.Printf("%s=== %s ===\n", indent, filename)
    fmt.Printf("%s%-40s %10s\n", indent, "文件名", "大小")
    fmt.Printf("%s%s\n", indent, strings.Repeat("-", 50))

    for _, f := range r.File {
        nestedIndent := strings.Repeat("  ", depth+1)

        if f.FileInfo().IsDir() {
            fmt.Printf("%s%-40s %10s\n", nestedIndent, f.Name+"/", "目录")
            continue
        }

        // 识别嵌套压缩包
        if _, err := detectFormat(f.Name); err == nil {
            fmt.Printf("%s%-40s %10d bytes [压缩包]\n", nestedIndent, f.Name, f.UncompressedSize64)

            // 创建临时文件处理嵌套压缩包，保留扩展名
            ext := filepath.Ext(f.Name)
            tmpFile, err := os.CreateTemp("", "unbox_*" + ext)
            if err != nil {
                log.Printf("创建临时文件失败: %v", err)
                continue
            }
            tmpFileName := tmpFile.Name()
            defer os.Remove(tmpFileName)
            defer tmpFile.Close()

            // 从ZIP提取嵌套压缩包到临时文件
            rc, err := f.Open()
            if err != nil {
                log.Printf("打开嵌套包失败: %v", err)
                continue
            }
            defer rc.Close()

            if _, err := io.Copy(tmpFile, rc); err != nil {
                log.Printf("写入临时文件失败: %v", err)
                continue
            }
            tmpFile.Close() // 必须关闭才能读取

            // 递归预览临时文件中的压缩包
            if err := listContents(tmpFileName, depth+1); err != nil {
                log.Printf("预览嵌套包失败: %v", err)
            }
        } else {
            fmt.Printf("%s%-40s %10d bytes\n", nestedIndent, f.Name, f.UncompressedSize64)
        }
    }
    return nil
}

func listTarContents(filename string, depth int, isGzipped, isBzipped, isXzed bool) error {
    file, err := os.Open(filename)
    if err != nil {
        return err
    }
    defer file.Close()

    var reader io.Reader = file
    if isGzipped {
        gzReader, err := gzip.NewReader(file)
        if err != nil {
            return err
        }
        defer gzReader.Close()
        reader = gzReader
    } else if isBzipped {
        reader = bzip2.NewReader(file)
    } else if isXzed {
        fmt.Printf("警告: XZ格式预览需要外部库支持")
        return nil
    }

    tarReader := tar.NewReader(reader)
    indent := strings.Repeat("  ", depth)
    fmt.Printf("%s=== %s ===\n", indent, filename)
    fmt.Printf("%s%-40s %10s\n", indent, "文件名", "类型")
    fmt.Printf("%s%s\n", indent, strings.Repeat("-", 50))

    for {
        header, err := tarReader.Next()
        if err == io.EOF {
            break
        }
        if err != nil {
            return err
        }

        nestedIndent := strings.Repeat("  ", depth+1)

        if header.Typeflag == tar.TypeDir {
            fmt.Printf("%s%-40s %10s\n", nestedIndent, header.Name+"/", "目录")
            continue
        }

        // 识别嵌套压缩包并递归预览
        if _, err := detectFormat(header.Name); err == nil {
            fmt.Printf("%s%-40s %10s [压缩包]\n", nestedIndent, header.Name, "文件")

            // 创建临时文件，保留扩展名
            ext := filepath.Ext(header.Name)
            tmpFile, err := os.CreateTemp("", "unbox_*" + ext)
            if err != nil {
                log.Printf("创建临时文件失败: %v", err)
                continue
            }
            tmpFileName := tmpFile.Name()
            defer os.Remove(tmpFileName)

            // 将当前文件内容写入临时文件
            if _, err := io.Copy(tmpFile, tarReader); err != nil {
                log.Printf("写入临时文件失败: %v", err)
                tmpFile.Close()
                continue
            }
            if err := tmpFile.Close(); err != nil {
                log.Printf("关闭临时文件失败: %v", err)
                continue
            }

            // 递归预览
            if err := listContents(tmpFileName, depth+1); err != nil {
                log.Printf("预览嵌套包失败: %v", err)
            }
        } else {
            fileType := "文件"
            if header.Typeflag == tar.TypeSymlink {
                fileType = "符号链接"
            }
            fmt.Printf("%s%-40s %10s\n", nestedIndent, header.Name, fileType)
        }
    }
    return nil
}

// ====================== 批量解压逻辑 ======================
func processFiles(files []string, recursiveFlag, deleteSource bool) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5) // 最大并发数限制

	for _, file := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ext, err := detectFormat(f)
			if err != nil {
				log.Printf("错误[%s]: %v", f, err)
				return
			}

			baseName := filepath.Base(f)
			outputDir := strings.TrimSuffix(baseName, ext)
			if err := os.MkdirAll(outputDir, 0755); err != nil {
				log.Printf("创建目录失败[%s]: %v", f, err)
				return
			}

			fmt.Printf("正在解压 %s 到 %s...\n", f, outputDir)
			if err := supportedFormats[ext](f, outputDir); err != nil {
				log.Printf("解压失败[%s]: %v", f, err)
				return
			}
			fmt.Printf("%s 解压完成!\n", f)

			if recursiveFlag {
				fmt.Printf("开始递归解压嵌套文件: %s\n", outputDir)
				if err := recursiveUnpackDir(outputDir, 0); err != nil {
					log.Printf("递归解压出错[%s]: %v", outputDir, err)
				}
			}

			if deleteSource {
				if err := os.Remove(f); err != nil {
					log.Printf("删除源文件失败[%s]: %v", f, err)
				} else {
					fmt.Printf("已删除源文件: %s\n", f)
				}
			}
		}(file)
	}
	wg.Wait()
}

// ====================== 命令行处理 ======================
func printHelp() {
    ansiReset := "\033[0m"
    fmt.Printf(`
%s[96m操作:%s
    %s[32m-r%s      递归解压嵌套压缩包
    %s[32m-l%s      列出压缩包内容（自动递归预览嵌套压缩包）
    %s[32m-o%s      解压后删除源文件
    %s[32m-h%s      显示这条帮助信息
    %s[32m-s%s      显示支持格式列表
    %s[32m--version%s 显示版本信息

%s[96m示例:%s
    %s[93munbox archive.zip backup.tar%s
    %s[93munbox -r archive.zip%s
    %s[93munbox -l file1.tar file2.zip%s
    %s[93munbox -o *.zip *.tar.gz%s
`,
        "\033", ansiReset,
        "\033", ansiReset,
        "\033", ansiReset,
        "\033", ansiReset,
        "\033", ansiReset,
        "\033", ansiReset,
        "\033", ansiReset,
        "\033", ansiReset,
        "\033", ansiReset,
        "\033", ansiReset,
        "\033", ansiReset,
        "\033", ansiReset,
    )
}

func main() {
	log.SetFlags(0) // 禁用日志时间戳
	flag.Parse()
	files := flag.Args()

	switch {
	case *version:
		// 应用渐变色输出
		coloredVersion := addGradient(versionText, [3]int{210, 58, 68}, [3]int{221, 155, 85})
		fmt.Println(coloredVersion)
	case *help:
		printHelp()
	case *supported:
		fmt.Println(supportedFormatsText)
	case *list:
		if len(files) == 0 {
			log.Fatal("请指定要预览的压缩文件")
		}
		for _, file := range files {
			if err := listContents(file, 0); err != nil {
				log.Printf("预览失败 [%s]: %v", file, err)
			}
		}
	default:
		if len(files) == 0 {
			log.Fatal("用法: unbox [选项] 压缩文件 [压缩文件2 ...]")
		}
		processFiles(files, *recursive, *deleteOrig)
	}
}

