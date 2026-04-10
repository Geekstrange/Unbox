package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// FileLocation 精确记录文件在归档中的位置，防止嵌套同名归档导致误判
type FileLocation struct {
	IsNested      bool
	NestedArchive string // 嵌套归档在主归档中的相对路径
	ItemPath      string // 文件在其所在归档中的相对路径
}

type Config struct {
	deleteOrigin   bool
	listContent    bool
	addFiles       []string
	deleteContent  bool
	extractContent bool
	contentMap     map[int]*FileLocation
	currentNumber  int
}

func main() {
	config := &Config{
		contentMap:    make(map[int]*FileLocation),
		currentNumber: 1,
	}

	args := os.Args[1:]
	if len(args) == 0 {
		showHelp()
		os.Exit(1)
	}

	files, err := parseArgs(args, config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "Error: No input files specified")
		showHelp()
		os.Exit(1)
	}

	// 1. Handle List mode (-l)
	if config.listContent {
		for _, file := range files {
			fmt.Println("==================================")
			if !isCompressedFile(file) {
				fmt.Fprintf(os.Stderr, "Error: '%s' is not a supported archive format\n", file)
				continue
			}
			fmt.Printf("Contents of %s:\n", file)
			if err := processList(file, config); err != nil {
				fmt.Fprintf(os.Stderr, "Error listing %s: %v\n", file, err)
			}
			fmt.Println("==================================")
		}
		return
	}

	// 2. Handle Delete content mode (-d)
	if config.deleteContent {
		if len(files) != 1 || len(config.addFiles) > 0 || config.deleteOrigin {
			fmt.Fprintln(os.Stderr, "Error: -d option can only be used alone with exactly one archive file")
			os.Exit(1)
		}
		if !isCompressedFile(files[0]) {
			fmt.Fprintf(os.Stderr, "Error: '%s' is not a supported archive format\n", files[0])
			os.Exit(1)
		}
		if err := processDelete(files[0], config); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 3. Handle Extract content mode (-e)
	if config.extractContent {
		if len(files) != 1 || len(config.addFiles) > 0 || config.deleteOrigin {
			fmt.Fprintln(os.Stderr, "Error: -e option can only be used alone with exactly one archive file")
			os.Exit(1)
		}
		if !isCompressedFile(files[0]) {
			fmt.Fprintf(os.Stderr, "Error: '%s' is not a supported archive format\n", files[0])
			os.Exit(1)
		}
		if err := processExtract(files[0], config); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 4. Handle Add files mode (-a)
	if len(config.addFiles) > 0 {
		if len(files) != 1 {
			fmt.Fprintln(os.Stderr, "Error: Add files mode requires exactly one archive file")
			os.Exit(1)
		}
		if config.deleteOrigin {
			fmt.Fprintln(os.Stderr, "Error: Add files mode cannot be used with -o options")
			os.Exit(1)
		}
		if err := addFilesToArchive(files[0], config.addFiles); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// 5. Default: Process all files (Extract all)
	for _, file := range files {
		fmt.Println("==================================")
		if err := processFile(file, config); err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %v\n", file, err)
		}
		fmt.Println("==================================")
	}
}

func showHelp() {
	// 直接使用字符串拼接硬编码 ANSI 颜色，避免 Printf 占位符数量不匹配的问题
	fmt.Print(`
` + "\033[96m" + `Options:` + "\033[0m" + `
    ` + "\033[32m" + `-o` + "\033[0m" + `      Delete original archive after successful extraction.
    ` + "\033[32m" + `-e` + "\033[0m" + `      Extract specific file from the archive.
    ` + "\033[32m" + `-l` + "\033[0m" + `      Display the contents of the archive.
    ` + "\033[32m" + `-a` + "\033[0m" + `      Add files to the archive.
    ` + "\033[32m" + `-d` + "\033[0m" + `      Delete file from the archive.
    ` + "\033[32m" + `-h` + "\033[0m" + `      Show this help message.
    ` + "\033[32m" + `-v` + "\033[0m" + `      Show version and license information.

` + "\033[96m" + `Examples:` + "\033[0m" + `
	` + "\033[93m" + `unbox -o *.zip *.tar.gz` + "\033[0m" + `
	` + "\033[93m" + `unbox -e archive` + "\033[0m" + `
	` + "\033[93m" + `unbox -l archive.zip` + "\033[0m" + `
	` + "\033[93m" + `unbox -a file archive.zip` + "\033[0m" + `
	` + "\033[93m" + `unbox -d archive.zip` + "\033[0m" + `
    
`)
}

const versionText = `
------------Unbox version 0.0.5------------
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

		if r < 0 { r = 0 } else if r > 255 { r = 255 }
		if g < 0 { g = 0 } else if g > 255 { g = 255 }
		if b < 0 { b = 0 } else if b > 255 { b = 255 }

		result.WriteString(fmt.Sprintf("\033[38;2;%d;%d;%dm%c", r, g, b, char))
	}
	return result.String() + "\033[0m"
}

func parseArgs(args []string, config *Config) ([]string, error) {
	var files []string
	i := 0

	for i < len(args) {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			files = append(files, arg)
			i++
			continue
		}

		switch arg {
		case "-o":
			config.deleteOrigin = true
		case "-e":
			config.extractContent = true
		case "-l":
			config.listContent = true
		case "-d":
			config.deleteContent = true
		case "-h":
			showHelp()
			os.Exit(0)
		case "-v":
			coloredVersion := addGradient(versionText, [3]int{210, 58, 68}, [3]int{221, 155, 85})
			fmt.Println(coloredVersion)
			os.Exit(0)
		case "-a":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("option -a requires an argument")
			}
			i++
			config.addFiles = append(config.addFiles, args[i])
		default:
			return nil, fmt.Errorf("invalid option: %s", arg)
		}
		i++
	}
	return files, nil
}

func commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

func isCompressedFile(filename string) bool {
	extensions := []string{
		".tar.bz2", ".tbz2", ".tar.gz", ".tgz", ".tar.xz", ".txz",
		".bz2", ".rar", ".gz", ".tar", ".zip", ".Z", ".7z", ".xz", ".lzma",
	}
	filename = strings.ToLower(filename)
	for _, ext := range extensions {
		if strings.HasSuffix(filename, ext) {
			return true
		}
	}
	return false
}

func extractArchive(file, dest string) error {
	if dest == "" {
		dest = "."
	}

	// 优先检查是否安装了 7z，因为我们将把它作为主要的解压引擎
	has7z := commandExists("7z")

	switch {
	// 1. Tar 家族：原生 tar 命令支持一步解压到底，体验最好（7z 解压 tar.gz 需要两步）
	case strings.HasSuffix(file, ".tar.bz2") || strings.HasSuffix(file, ".tbz2"):
		return runCommand("tar", "xjf", file, "-C", dest)
	case strings.HasSuffix(file, ".tar.gz") || strings.HasSuffix(file, ".tgz"):
		return runCommand("tar", "xzf", file, "-C", dest)
	case strings.HasSuffix(file, ".tar.xz") || strings.HasSuffix(file, ".txz"):
		return runCommand("tar", "xJf", file, "-C", dest)
	case strings.HasSuffix(file, ".tar"):
		return runCommand("tar", "xf", file, "-C", dest)

	// 2. 其他所有格式：统统交给 7z
	default:
		if !has7z {
			return fmt.Errorf("7z command is required to extract '%s', please install p7zip", filepath.Base(file))
		}
		// 7z x: 保持目录结构解压
		// -y: 遇到提示自动选 yes，防止卡在终端等待输入
		// -o: 指定输出目录（注意：-o 和路径之间没有空格）
		return runCommand("7z", "x", "-y", file, "-o"+dest)
	}
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	// Suppress output for clean tree view
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCommandInDir(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil { return err }
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil { return err }
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func createTempDir(prefix string) (string, error) {
	return os.MkdirTemp("", prefix)
}

// ============== 核心：统一的树状遍历引擎 ==============
func buildArchiveTree(currentExtractDir string, currentRelPath string, prefix string, config *Config, nestedArchivePath string) error {
	entries, err := os.ReadDir(currentExtractDir)
	if err != nil {
		return err
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return entries[i].Name() < entries[j].Name()
	})

	numItems := len(entries)
	for i, entry := range entries {
		itemName := entry.Name()
		fullPath := filepath.Join(currentExtractDir, itemName)

		itemRelPath := itemName
		if currentRelPath != "" {
			itemRelPath = filepath.Join(currentRelPath, itemName)
		}

		var linePrefix, newPrefix string
		if i == numItems-1 {
			linePrefix = prefix + "└── "
			newPrefix = prefix + "    "
		} else {
			linePrefix = prefix + "├── "
			newPrefix = prefix + "│   "
		}

		if entry.IsDir() {
			fmt.Printf("%s\033[34m%s/\033[0m\n", linePrefix, itemName)
			buildArchiveTree(fullPath, itemRelPath, newPrefix, config, nestedArchivePath)
		} else {
			isNestedArchive := isCompressedFile(itemName)

			loc := &FileLocation{
				IsNested:      nestedArchivePath != "",
				NestedArchive: nestedArchivePath,
				ItemPath:      itemRelPath,
			}
			config.contentMap[config.currentNumber] = loc

			if isNestedArchive && nestedArchivePath == "" {
				fmt.Printf("%s%d) \033[36m%s\033[0m [Nested Archive]\n", linePrefix, config.currentNumber, itemName)
				config.currentNumber++

				nestedTmp, err := createTempDir("ub_nest_")
				if err == nil {
					if extractArchive(fullPath, nestedTmp) == nil {
						buildArchiveTree(nestedTmp, "", newPrefix, config, itemRelPath)
					}
					os.RemoveAll(nestedTmp)
				}
			} else {
				fmt.Printf("%s%d) %s\n", linePrefix, config.currentNumber, itemName)
				config.currentNumber++
			}
		}
	}
	return nil
}

func processList(archive string, config *Config) error {
	config.contentMap = make(map[int]*FileLocation)
	config.currentNumber = 1

	tmpdir, err := createTempDir("ub_list_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpdir)

	if err := extractArchive(archive, tmpdir); err != nil {
		return fmt.Errorf("extraction failed: %v", err)
	}

	return buildArchiveTree(tmpdir, "", "", config, "")
}

// ============== Delete 逻辑 ==============
func processDelete(archive string, config *Config) error {
	fmt.Println("Listing archive contents:")
	if err := processList(archive, config); err != nil {
		return err
	}

	if len(config.contentMap) == 0 {
		fmt.Println("Archive is empty, no content to delete")
		return nil
	}

	fmt.Print("Enter the number(s) to delete (space separated): ")
	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return err
	}

	numbers := strings.Fields(input)
	var filesToDelete []*FileLocation

	for _, numStr := range numbers {
		num, err := strconv.Atoi(numStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: '%s' is invalid, skipping\n", numStr)
			continue
		}
		if entry, exists := config.contentMap[num]; exists {
			filesToDelete = append(filesToDelete, entry)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: number '%d' does not exist\n", num)
		}
	}

	if len(filesToDelete) == 0 {
		fmt.Println("No valid files to delete")
		return nil
	}

	return deleteFilesFromArchive(archive, filesToDelete)
}

func deleteFilesFromArchive(mainArchive string, filesToDelete []*FileLocation) error {
	mainTmpdir, err := createTempDir("ub_del_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mainTmpdir)

	if err := extractArchive(mainArchive, mainTmpdir); err != nil {
		return fmt.Errorf("failed to extract main archive: %v", err)
	}

	for _, loc := range filesToDelete {
		if loc.IsNested {
			nestedFileMainPath := filepath.Join(mainTmpdir, loc.NestedArchive)
			nestedTmpdir, err := createTempDir("ub_nest_del_")
			if err != nil { continue }

			if extractArchive(nestedFileMainPath, nestedTmpdir) == nil {
				fileToDelete := filepath.Join(nestedTmpdir, loc.ItemPath)
				if err := os.RemoveAll(fileToDelete); err == nil {
					fmt.Printf("Deleted nested file: %s\n", loc.ItemPath)
					os.Remove(nestedFileMainPath)
					compressArchive(nestedFileMainPath, nestedTmpdir)
				}
			}
			os.RemoveAll(nestedTmpdir)
		} else {
			fileToDelete := filepath.Join(mainTmpdir, loc.ItemPath)
			if err := os.RemoveAll(fileToDelete); err == nil {
				fmt.Printf("Deleted file: %s\n", loc.ItemPath)
			}
		}
	}

	fmt.Printf("Recompressing main archive: %s\n", mainArchive)
	if err := compressArchive(mainArchive, mainTmpdir); err != nil {
		return err
	}
	fmt.Println("Delete operation completed")
	return nil
}

// ============== Extract 逻辑 ==============
func processExtract(archive string, config *Config) error {
	fmt.Println("Listing archive contents:")
	if err := processList(archive, config); err != nil {
		return err
	}

	if len(config.contentMap) == 0 {
		fmt.Println("Archive is empty, no content to extract")
		return nil
	}

	fmt.Print("Enter the number(s) to extract (space separated): ")
	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return err
	}

	numbers := strings.Fields(input)
	var filesToExtract []*FileLocation

	for _, numStr := range numbers {
		num, err := strconv.Atoi(numStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: '%s' is invalid, skipping\n", numStr)
			continue
		}
		if entry, exists := config.contentMap[num]; exists {
			filesToExtract = append(filesToExtract, entry)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: number '%d' does not exist\n", num)
		}
	}

	if len(filesToExtract) == 0 {
		fmt.Println("No valid files to extract")
		return nil
	}

	return extractSelectedFiles(archive, filesToExtract)
}

func extractSelectedFiles(mainArchive string, filesToExtract []*FileLocation) error {
	mainTmpdir, err := createTempDir("ub_ext_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mainTmpdir)

	if err := extractArchive(mainArchive, mainTmpdir); err != nil {
		return fmt.Errorf("failed to extract main archive: %v", err)
	}

	for _, loc := range filesToExtract {
		var sourceFile string
		if loc.IsNested {
			nestedFileMainPath := filepath.Join(mainTmpdir, loc.NestedArchive)
			nestedTmpdir, err := createTempDir("ub_nest_ext_")
			if err != nil { continue }

			if extractArchive(nestedFileMainPath, nestedTmpdir) == nil {
				sourceFile = filepath.Join(nestedTmpdir, loc.ItemPath)
				destFile := filepath.Join(".", filepath.Base(loc.ItemPath))
				
				if err := os.MkdirAll(filepath.Dir(destFile), 0755); err == nil {
					if err := copyFile(sourceFile, destFile); err == nil {
						fmt.Printf("Extracted: %s\n", destFile)
					}
				}
			}
			os.RemoveAll(nestedTmpdir)
		} else {
			sourceFile = filepath.Join(mainTmpdir, loc.ItemPath)
			destFile := filepath.Join(".", loc.ItemPath)

			if err := os.MkdirAll(filepath.Dir(destFile), 0755); err == nil {
				if err := copyFile(sourceFile, destFile); err == nil {
					fmt.Printf("Extracted: %s\n", destFile)
				}
			}
		}
	}

	fmt.Println("Extract operation completed")
	return nil
}

// ============== 通用逻辑 ==============
func addFilesToArchive(archive string, filesToAdd []string) error {
	absArchive, err := filepath.Abs(archive)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path for archive: %v", err)
	}

	tmpdir, err := createTempDir("ub_add_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpdir)

	if err := extractArchive(archive, tmpdir); err != nil {
		return fmt.Errorf("extraction failed, cannot add files: %v", err)
	}

	// 新增：记录真实添加成功的文件数量
	addedCount := 0

	for _, file := range filesToAdd {
		absFile, _ := filepath.Abs(file)
		if absFile == absArchive {
			fmt.Fprintf(os.Stderr, "Warning: cannot add archive '%s' to itself, skipping\n", file)
			continue
		}

		if _, err := os.Stat(file); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: file '%s' does not exist, skipping\n", file)
			continue
		}
		dst := filepath.Join(tmpdir, filepath.Base(file))
		if err := copyFile(file, dst); err == nil {
			fmt.Printf("Copied: %s\n", file)
			addedCount++ // 新增：复制成功，计数器 +1
		}
	}

	// 新增：拦截器。如果没有任何文件被成功复制，直接退出，不要重压缩
	if addedCount == 0 {
		fmt.Println("No new files were added. Archive remains unchanged.")
		return nil
	}

	fmt.Printf("Recompressing to: %s\n", archive)
	if err := compressArchive(absArchive, tmpdir); err != nil {
		return err
	}

	fmt.Println("Files added successfully")
	return nil
}

func compressArchive(archive, sourceDir string) error {
	// 无论上层传入什么，强制转换为绝对路径，保证安全
	absArchive, err := filepath.Abs(archive)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %v", err)
	}

	if err := os.Remove(absArchive); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove original archive: %v", err)
	}

	// 命令全部使用绝对路径 absArchive 进行输出
	switch {
	case strings.HasSuffix(archive, ".zip"):
		return runCommandInDir(sourceDir, "zip", "-qr", absArchive, ".")
	case strings.HasSuffix(archive, ".tar"):
		return runCommand("tar", "cf", absArchive, "-C", sourceDir, ".")
	case strings.HasSuffix(archive, ".tar.gz") || strings.HasSuffix(archive, ".tgz"):
		return runCommand("tar", "czf", absArchive, "-C", sourceDir, ".")
	case strings.HasSuffix(archive, ".tar.bz2") || strings.HasSuffix(archive, ".tbz2"):
		return runCommand("tar", "cjf", absArchive, "-C", sourceDir, ".")
	case strings.HasSuffix(archive, ".tar.xz") || strings.HasSuffix(archive, ".txz"):
		return runCommand("tar", "cJf", absArchive, "-C", sourceDir, ".")
	case strings.HasSuffix(archive, ".7z"):
		return runCommand("7z", "a", absArchive, sourceDir+"/.")
	default:
		return fmt.Errorf("unsupported format for adding files: %s", archive)
	}
}

func processFile(file string, config *Config) error {
	if _, err := os.Stat(file); err != nil {
		return fmt.Errorf("'%s' is not a valid file", file)
	}

	fmt.Printf("Extracting: %s\n", file)
	if err := extractArchive(file, "."); err != nil {
		return err
	}

	// Simple interactive deletion for full extraction mode
	if config.deleteOrigin {
		fmt.Printf("Delete original archive %s? (y/n): ", file)
		reader := bufio.NewReader(os.Stdin)
		ans, _ := reader.ReadString('\n')
		ans = strings.TrimSpace(strings.ToLower(ans))
		if ans == "y" || ans == "yes" {
			os.Remove(file)
			fmt.Printf("Removed: %s\n", file)
		}
	}

	return nil
}