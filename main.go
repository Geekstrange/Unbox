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

// 支持格式映射表（扩展名 -> 处理函数）
var supportedFormats = map[string]func(string, string) error{
	".zip":    extractZip,
	".tar":    extractTar,
	".tar.gz": extractTarGz,
	".gz":     extractGzip,
	".bz2":    extractBzip2,
	".zst":    extractZstd,
	".rar":    extractRar,
	".7z":     extract7z,
	// 其他格式处理函数
}

// 命令行参数
var (
	recursive  = flag.Bool("r", false, "递归解压嵌套压缩包")
	list       = flag.Bool("l", false, "列出压缩包内容")
	deleteOrig = flag.Bool("o", false, "解压后删除源文件")
	version    = flag.Bool("version", false, "显示版本信息")
	help       = flag.Bool("h", false, "显示帮助信息")
	supported  = flag.Bool("s", false, "显示支持格式列表")
)

const (
	versionText = `Unbox version 0.0.1
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

	supportedFormatsText = `7z
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

// ====================== 核心解压函数 ======================
// ZIP解压
func extractZip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		// 安全校验：防止路径遍历攻击
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

// TAR解压
func extractTar(src, dest string) error {
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer file.Close()

	return extractTarStream(tar.NewReader(file), dest)
}

// TAR.GZ解压
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

// GZIP解压
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

	// 生成解压后文件名（去掉.gz后缀）
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

// BZIP2解压
func extractBzip2(src, dest string) error {
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer file.Close()

	bzReader := bzip2.NewReader(file)

	// 生成解压后文件名（去掉.bz2后缀）
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

// RAR解压（需系统安装unrar）
func extractRar(src, dest string) error {
	cmd := exec.Command("unrar", "x", "-o+", src, dest)
	return cmd.Run()
}

// 7Z解压（需系统安装7z）
func extract7z(src, dest string) error {
	cmd := exec.Command("7z", "x", "-o"+dest, src)
	return cmd.Run()
}

// Zstandard解压（需系统安装zstd）
func extractZstd(src, dest string) error {
	// 生成解压后文件名（去掉.zst后缀）
	baseName := filepath.Base(src)
	targetName := strings.TrimSuffix(baseName, ".zst")
	targetPath := filepath.Join(dest, targetName)

	cmd := exec.Command("zstd", "-d", src, "-o", targetPath)
	return cmd.Run()
}

// ====================== 辅助函数 ======================
// 通用TAR流处理
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

// 检测文件格式
func detectFormat(filename string) (string, error) {
	ext := filepath.Ext(filename)
	if _, exists := supportedFormats[ext]; exists {
		return ext, nil
	}

	// 处理复合扩展名（如.tar.gz）
	if secondExt := filepath.Ext(strings.TrimSuffix(filename, ext)); secondExt != "" {
		compoundExt := secondExt + ext
		if _, exists := supportedFormats[compoundExt]; exists {
			return compoundExt, nil
		}
	}

	return "", fmt.Errorf("unsupported format: %s", ext)
}

// 递归解压实现
func recursiveUnpack(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() || err != nil {
			return nil
		}

		if ext, err := detectFormat(path); err == nil {
			fmt.Printf("发现嵌套压缩包: %s\n", path)
			outputDir := filepath.Dir(path)
			if err := supportedFormats[ext](path, outputDir); err != nil {
				fmt.Printf("解压失败: %v\n", err)
				return nil
			}
			os.Remove(path) // 解压后删除嵌套压缩包
			fmt.Printf("已解压并删除: %s\n", path)
		}
		return nil
	})
}

// 列出压缩包内容
func listContents(filename string) error {
	ext, err := detectFormat(filename)
	if err != nil {
		return err
	}

	switch ext {
	case ".zip":
		return listZipContents(filename)
	case ".tar", ".tar.gz":
		return listTarContents(filename, ext == ".tar.gz")
	default:
		return fmt.Errorf("预览不支持此格式: %s", ext)
	}
}

func listZipContents(filename string) error {
	r, err := zip.OpenReader(filename)
	if err != nil {
		return err
	}
	defer r.Close()

	fmt.Printf("%-40s %10s\n", "文件名", "大小")
	fmt.Println(strings.Repeat("-", 50))
	for _, f := range r.File {
		fmt.Printf("%-40s %10d bytes\n", f.Name, f.UncompressedSize64)
	}
	return nil
}

func listTarContents(filename string, isGzipped bool) error {
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
	}

	tarReader := tar.NewReader(reader)
	fmt.Printf("%-40s %10s\n", "文件名", "类型")
	fmt.Println(strings.Repeat("-", 50))
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		fileType := "文件"
		if header.Typeflag == tar.TypeDir {
			fileType = "目录"
		}
		fmt.Printf("%-40s %10s\n", header.Name, fileType)
	}
	return nil
}

// ====================== 批量解压逻辑 ======================
func processFiles(files []string, recursiveFlag, deleteSource bool) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5) // 最大并发数限制为5

	for _, file := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			sem <- struct{}{}        // 获取信号量
			defer func() { <-sem }() // 释放信号量

			// 1. 检测文件格式
			ext, err := detectFormat(f)
			if err != nil {
				log.Printf("错误[%s]: %v", f, err)
				return
			}

			// 2. 创建解压目录
			baseName := filepath.Base(f)
			outputDir := strings.TrimSuffix(baseName, ext)
			if err := os.MkdirAll(outputDir, 0755); err != nil {
				log.Printf("创建目录失败[%s]: %v", f, err)
				return
			}

			// 3. 执行解压
			fmt.Printf("正在解压 %s 到 %s...\n", f, outputDir)
			if err := supportedFormats[ext](f, outputDir); err != nil {
				log.Printf("解压失败[%s]: %v", f, err)
				return
			}
			fmt.Printf("%s 解压完成!\n", f)

			// 4. 递归解压
			if recursiveFlag {
				fmt.Printf("开始递归解压嵌套文件: %s\n", outputDir)
				if err := recursiveUnpack(outputDir); err != nil {
					log.Printf("递归解压出错[%s]: %v", outputDir, err)
				}
			}

			// 5. 删除源文件
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
	fmt.Println(`Unbox - 智能解压工具
用法: unbox [选项] 压缩文件 [压缩文件2 ...]

选项:
  -f    解压文件 (默认操作)
  -r    递归解压嵌套压缩包
  -l    列出压缩包内容
  -o    解压后删除源文件
  -h    显示这条帮助信息
  -s    显示支持格式列表
  --version 显示版本信息

示例:
  unbox -f archive.zip backup.tar
  unbox -rl nested.tar.gz data.zip
  unbox -l file1.tar file2.zip
  unbox -o *.zip *.tar.gz`)
}

func main() {
	log.SetFlags(0)

	flag.Parse()
	files := flag.Args() // 获取所有非选项参数（压缩文件列表）

	switch {
	case *version:
		fmt.Println(versionText)
	case *help:
		printHelp()
	case *supported:
		fmt.Println(supportedFormatsText)
	case *list:
		if len(files) == 0 {
			log.Fatal("请指定要预览的压缩文件")
		}
		for _, file := range files {
			fmt.Printf("=== %s ===\n", file)
			if err := listContents(file); err != nil {
				log.Printf("预览失败 [%s]: %v", file, err)
			}
		}
	default: // 默认执行解压操作
		if len(files) == 0 {
			log.Fatal("用法: unbox [选项] 压缩文件 [压缩文件2 ...]")
		}
		processFiles(files, *recursive, *deleteOrig)
	}
}
