package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// FileLocation 精确记录文件在归档中的位置，支持任意深度嵌套
type FileLocation struct {
	Chain    []string // 各级嵌套归档在其父归档中的相对路径；空表示文件直接位于主归档
	ItemPath string   // 文件在最深层归档中的相对路径
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
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Print("\033[0m")
		os.Exit(1)
	}()

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
			fmt.Println("----------------------------------")
			fmt.Printf("Contents of %s:\n", file)
			if err := processList(file, config); err != nil {
				fmt.Fprintf(os.Stderr, "Error listing %s: %v\n", file, err)
			}
			fmt.Println("----------------------------------")
		}
		return
	}

	// 2. Handle Delete content mode (-d)
	if config.deleteContent {
		if len(files) != 1 || len(config.addFiles) > 0 || config.deleteOrigin {
			fmt.Fprintln(os.Stderr, "Error: -d option can only be used alone with exactly one archive file")
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
		fmt.Println("----------------------------------")
		if err := processFile(file, config); err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %v\n", file, err)
		}
		fmt.Println("----------------------------------")
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
	` + "\033[93m" + `unbox -e archive.zip` + "\033[0m" + `
	` + "\033[93m" + `unbox -l archive.zip` + "\033[0m" + `
	` + "\033[93m" + `unbox -a file archive.zip` + "\033[0m" + `
	` + "\033[93m" + `unbox -d archive.zip` + "\033[0m" + `

`)
}

const versionText = `
------------Unbox version 0.0.8------------
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

// GNU tar 提供 --delete 原生删除能力；bsdtar 不支持
var tarSupportsDelete = func() bool {
	out, err := exec.Command("tar", "--help").CombinedOutput()
	return err == nil && strings.Contains(string(out), "--delete")
}()

const maxNestDepth = 16 // 嵌套展开深度上限，防止自引用归档无限递归

// safeJoin 将归档内相对路径安全拼接到 base 下，拒绝绝对路径与 .. 穿越
func safeJoin(base, rel string) (string, error) {
	if rel == "" {
		return base, nil
	}
	if filepath.IsAbs(rel) || strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("unsafe path in archive: %q", rel)
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe path in archive: %q", rel)
	}
	return filepath.Join(base, clean), nil
}

// canWriteFormat 校验归档格式支持修改（增删需要重压缩）
func canWriteFormat(archive string) error {
	lower := strings.ToLower(archive)
	writable := []string{
		".zip", ".7z",
		".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tar.xz", ".txz", ".tar.zst", ".tzst",
	}
	for _, ext := range writable {
		if strings.HasSuffix(lower, ext) {
			return nil
		}
	}
	return fmt.Errorf("format '%s' does not support modification", filepath.Base(archive))
}

// makeArchiveTemp 在原归档同目录生成唯一的临时文件路径（同目录保证 rename 原子性）
func makeArchiveTemp(archive string) (string, error) {
	absArchive, err := filepath.Abs(archive)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(absArchive), filepath.Base(absArchive)+".tmp-*")
	if err != nil {
		return "", err
	}
	// 追加原扩展名，兼容按扩展名识别格式的工具（如 7z）
	tmpPath := f.Name() + filepath.Ext(absArchive)
	f.Close()
	os.Remove(f.Name())
	return tmpPath, nil
}

// topLevelNames 列出目录全部顶层条目名，压缩时逐项传入避免归档出现 "./" 前缀条目
func topLevelNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// stdinReader 全局共享的标准输入读取器：
// 多个独立的 bufio.Reader 会各自预读 stdin，导致后续提示读不到输入
var stdinReader = bufio.NewReader(os.Stdin)

// promptYN 打印问题并等待 y/yes 确认
func promptYN(question string) bool {
	fmt.Printf("%s (y/n): ", question)
	ans, _ := stdinReader.ReadString('\n')
	ans = strings.TrimSpace(strings.ToLower(ans))
	return ans == "y" || ans == "yes"
}

// locDisplayPath 生成文件在归档中的可读路径（含嵌套链）
func locDisplayPath(loc *FileLocation) string {
	if len(loc.Chain) == 0 {
		return loc.ItemPath
	}
	return strings.Join(loc.Chain, "/") + " → " + loc.ItemPath
}

// printSelection 打印待处理文件清单
func printSelection(locs []*FileLocation) {
	for _, loc := range locs {
		fmt.Printf("  - %s\n", locDisplayPath(loc))
	}
}

// parseSelection 解析用户选择：支持空格/逗号分隔的编号、N-M 范围与 all；非法输入警告并跳过
func parseSelection(input string, maxNum int) map[int]bool {
	selected := make(map[int]bool)
	for _, token := range strings.FieldsFunc(input, func(r rune) bool {
		return r == ' ' || r == ',' || r == '\n'
	}) {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if token == "all" {
			for n := 1; n <= maxNum; n++ {
				selected[n] = true
			}
			continue
		}
		if strings.Contains(token, "-") {
			parts := strings.SplitN(token, "-", 2)
			start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
			end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err1 != nil || err2 != nil || end < start {
				fmt.Fprintf(os.Stderr, "Warning: '%s' is invalid, skipping\n", token)
				continue
			}
			for n := start; n <= end; n++ {
				if n >= 1 && n <= maxNum {
					selected[n] = true
				}
			}
			continue
		}
		num, err := strconv.Atoi(token)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: '%s' is invalid, skipping\n", token)
			continue
		}
		if num >= 1 && num <= maxNum {
			selected[num] = true
		} else {
			fmt.Fprintf(os.Stderr, "Warning: number '%d' does not exist\n", num)
		}
	}
	return selected
}

// readSelection 读取一行用户输入，解析为按编号升序的 FileLocation 列表
func readSelection(config *Config) ([]*FileLocation, error) {
	input, err := stdinReader.ReadString('\n')
	if err != nil {
		return nil, err
	}

	selected := parseSelection(input, len(config.contentMap))
	nums := make([]int, 0, len(selected))
	for num := range selected {
		nums = append(nums, num)
	}
	sort.Ints(nums)

	locs := make([]*FileLocation, 0, len(nums))
	for _, num := range nums {
		locs = append(locs, config.contentMap[num])
	}
	return locs, nil
}

func isCompressedFile(filename string) bool {
	extensions := []string{
		".tar.bz2", ".tbz2", ".tar.gz", ".tgz", ".tar.xz", ".txz", ".tar.zst", ".tzst",
		".bz2", ".rar", ".gz", ".tar", ".zip", ".Z", ".7z", ".xz", ".lzma", ".zst",
		".cab", ".iso", ".arj", ".lzh", ".cpio", ".rpm", ".deb", ".dmg", ".wim", ".vhd",
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
	case strings.HasSuffix(file, ".tar.zst") || strings.HasSuffix(file, ".tzst"):
		return runCommand("tar", "--zstd", "-xf", file, "-C", dest)
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

// runCommandInDirArgs 同 runCommandInDir，但参数以完整切片传入
func runCommandInDirArgs(dir, name string, args []string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func createTempDir(prefix string) (string, error) {
	return os.MkdirTemp("", prefix)
}

// ============== 核心：统一的树状遍历引擎 ==============
func buildArchiveTree(currentExtractDir string, currentRelPath string, prefix string, config *Config, chain []string) error {
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
			linePrefix = prefix + "╰─ "
			newPrefix = prefix + "    "
		} else {
			linePrefix = prefix + "├─ "
			newPrefix = prefix + "│   "
		}

		if entry.IsDir() {
			fmt.Printf("\033[90m%s\033[34m%s/\033[0m\n", linePrefix, itemName)
			buildArchiveTree(fullPath, itemRelPath, newPrefix, config, chain)
		} else {
			isNestedArchive := isCompressedFile(itemName)

			loc := &FileLocation{
				Chain:    chain,
				ItemPath: itemRelPath,
			}
			config.contentMap[config.currentNumber] = loc

			if isNestedArchive {
				fmt.Printf("\033[90m%s\033[0m%d) \033[36m%s\033[0m [Nested Archive]\n", linePrefix, config.currentNumber, itemName)
				config.currentNumber++

				if len(chain) >= maxNestDepth {
					fmt.Printf("\033[90m%s\033[0m  (嵌套深度超过 %d 层，停止展开)\n", newPrefix, maxNestDepth)
					continue
				}

				nestedTmp, err := createTempDir("ub_nest_")
				if err != nil {
					fmt.Printf("Warning: failed to create temp dir for %s: %v\n", itemName, err)
					continue
				}
				if err := extractArchive(fullPath, nestedTmp); err != nil {
					fmt.Printf("Warning: failed to read nested archive %s: %v\n", itemName, err)
					os.RemoveAll(nestedTmp)
					continue
				}
				childChain := make([]string, len(chain)+1)
				copy(childChain, chain)
				childChain[len(chain)] = itemRelPath
				buildArchiveTree(nestedTmp, "", newPrefix, config, childChain)
				os.RemoveAll(nestedTmp)
			} else {
				fmt.Printf("\033[90m%s\033[0m%d) %s\n", linePrefix, config.currentNumber, itemName)
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

	return buildArchiveTree(tmpdir, "", "", config, nil)
}

// ============== Delete 逻辑 ==============
func processDelete(archive string, config *Config) error {
	if err := canWriteFormat(archive); err != nil {
		return err
	}

	fmt.Println("Listing archive contents:")
	if err := processList(archive, config); err != nil {
		return err
	}

	if len(config.contentMap) == 0 {
		fmt.Println("Archive is empty, no content to delete")
		return nil
	}

	fmt.Print("Enter the number(s) to delete (e.g. '1 3' or '1-5' or 'all'): ")
	filesToDelete, err := readSelection(config)
	if err != nil {
		return err
	}

	if len(filesToDelete) == 0 {
		fmt.Println("No valid files to delete")
		return nil
	}

	fmt.Println("Files to delete:")
	printSelection(filesToDelete)
	if !promptYN("Proceed with deletion?") {
		fmt.Println("Deletion cancelled")
		return nil
	}

	return deleteFilesFromArchive(archive, filesToDelete)
}

// allDirect 判断所有待删除文件是否都直接位于主归档内（无嵌套链）
func allDirect(files []*FileLocation) bool {
	for _, loc := range files {
		if len(loc.Chain) > 0 {
			return false
		}
	}
	return true
}

// canNativeDelete 判断归档是否支持系统工具原生删除（免全量解压重压）
func canNativeDelete(archive string) bool {
	lower := strings.ToLower(archive)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return commandExists("zip") || commandExists("7z")
	case strings.HasSuffix(lower, ".7z"):
		return commandExists("7z")
	case strings.HasSuffix(lower, ".tar"):
		return tarSupportsDelete
	}
	return false
}

// nativeDelete 用系统工具直接删除归档内条目（zip -d / 7z d / tar --delete），
// 先在副本上操作再原子替换，失败时原归档完好
func nativeDelete(archive string, filesToDelete []*FileLocation) error {
	tmpPath, err := makeArchiveTemp(archive)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)

	absArchive, err := filepath.Abs(archive)
	if err != nil {
		return err
	}
	if err := copyFile(absArchive, tmpPath); err != nil {
		return err
	}

	items := make([]string, 0, len(filesToDelete))
	for _, loc := range filesToDelete {
		items = append(items, loc.ItemPath)
	}

	lower := strings.ToLower(archive)
	var runErr error
	switch {
	case strings.HasSuffix(lower, ".zip"):
		if commandExists("zip") {
			args := append([]string{"-d", tmpPath}, items...)
			runErr = runCommand("zip", args...)
		} else {
			args := append([]string{"d", "-tzip", tmpPath}, items...)
			runErr = runCommand("7z", args...)
		}
	case strings.HasSuffix(lower, ".7z"):
		args := append([]string{"d", "-t7z", tmpPath}, items...)
		runErr = runCommand("7z", args...)
	case strings.HasSuffix(lower, ".tar"):
		args := append([]string{"--delete", "-f", tmpPath}, items...)
		runErr = runCommand("tar", args...)
	default:
		runErr = fmt.Errorf("unsupported format for native delete: %s", archive)
	}
	if runErr != nil {
		return runErr
	}

	if err := os.Rename(tmpPath, absArchive); err != nil {
		return fmt.Errorf("failed to replace archive: %v", err)
	}
	for _, loc := range filesToDelete {
		fmt.Printf("Deleted: %s\n", loc.ItemPath)
	}
	return nil
}

func deleteFilesFromArchive(mainArchive string, filesToDelete []*FileLocation) error {
	if len(filesToDelete) == 0 {
		return nil
	}

	// 原生删除快路径：全部条目都直接位于主归档内，直接用系统工具删除
	if allDirect(filesToDelete) && canNativeDelete(mainArchive) {
		if err := nativeDelete(mainArchive, filesToDelete); err != nil {
			// 原生删除失败不致命（如名称含通配符字符），回退全量路径，原归档未动
			fmt.Printf("Warning: native delete failed (%v), falling back to full recompress\n", err)
		} else {
			return nil
		}
	}

	mainTmpdir, err := createTempDir("ub_del_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mainTmpdir)

	if err := extractArchive(mainArchive, mainTmpdir); err != nil {
		return fmt.Errorf("failed to extract main archive: %v", err)
	}

	// 按嵌套链分组：同一嵌套归档内的删除合并为一次解压与重压
	direct := make([]*FileLocation, 0)
	byChain := make(map[string][]*FileLocation)
	var chains []string
	for _, loc := range filesToDelete {
		if len(loc.Chain) == 0 {
			direct = append(direct, loc)
			continue
		}
		key := strings.Join(loc.Chain, "\x00")
		if _, exists := byChain[key]; !exists {
			chains = append(chains, key)
		}
		byChain[key] = append(byChain[key], loc)
	}

	for _, key := range chains {
		group := byChain[key]
		if err := deleteFromChain(mainTmpdir, group[0].Chain, group); err != nil {
			return err
		}
	}

	for _, loc := range direct {
		fileToDelete, err := safeJoin(mainTmpdir, loc.ItemPath)
		if err != nil {
			fmt.Printf("Warning: %v\n", err)
			continue
		}
		if err := os.RemoveAll(fileToDelete); err != nil {
			fmt.Printf("Warning: failed to delete %s: %v\n", loc.ItemPath, err)
		} else {
			fmt.Printf("Deleted: %s\n", loc.ItemPath)
		}
	}

	fmt.Printf("Recompressing main archive: %s\n", mainArchive)
	if err := compressArchive(mainArchive, mainTmpdir); err != nil {
		return err
	}
	fmt.Println("Delete operation completed")
	return nil
}

// deleteFromChain 从嵌套链最深层归档删除文件，并逐级原子重压回父级
func deleteFromChain(mainTmpdir string, chain []string, group []*FileLocation) error {
	// tmps[i] 是 chain[i] 解压后的内容目录；tmps[0] 即主归档解压目录
	tmps := []string{mainTmpdir}
	cur := mainTmpdir
	cleanup := func() {
		for _, t := range tmps[1:] {
			os.RemoveAll(t)
		}
	}

	for _, archiveRel := range chain {
		nestedPath, err := safeJoin(cur, archiveRel)
		if err != nil {
			cleanup()
			return err
		}
		tmp, err := createTempDir("ub_nest_del_")
		if err != nil {
			cleanup()
			return err
		}
		if err := extractArchive(nestedPath, tmp); err != nil {
			os.RemoveAll(tmp)
			cleanup()
			return fmt.Errorf("failed to extract nested archive %s: %v", archiveRel, err)
		}
		tmps = append(tmps, tmp)
		cur = tmp
	}

	for _, loc := range group {
		fileToDelete, err := safeJoin(cur, loc.ItemPath)
		if err != nil {
			fmt.Printf("Warning: %v\n", err)
			continue
		}
		if err := os.RemoveAll(fileToDelete); err != nil {
			fmt.Printf("Warning: failed to delete %s: %v\n", loc.ItemPath, err)
		} else {
			fmt.Printf("Deleted: %s\n", loc.ItemPath)
		}
	}

	// 从最深层向最浅层重压回父级，任一级失败都中止且不影响原归档
	for i := len(tmps) - 1; i >= 1; i-- {
		archivePath, err := safeJoin(tmps[i-1], chain[i-1])
		if err != nil {
			cleanup()
			return err
		}
		if err := compressArchive(archivePath, tmps[i]); err != nil {
			cleanup()
			return fmt.Errorf("failed to recompress nested archive %s: %v", chain[i-1], err)
		}
		os.RemoveAll(tmps[i])
	}
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

	fmt.Print("Enter the number(s) to extract (e.g. '1 3' or '1-5' or 'all'): ")
	filesToExtract, err := readSelection(config)
	if err != nil {
		return err
	}

	if len(filesToExtract) == 0 {
		fmt.Println("No valid files to extract")
		return nil
	}

	fmt.Println("Files to extract:")
	printSelection(filesToExtract)

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
		// 沿嵌套链逐级解压；defer 按 LIFO 逆序清理各级临时目录
		cur := mainTmpdir
		ok := true
		for _, archiveRel := range loc.Chain {
			nestedPath, err := safeJoin(cur, archiveRel)
			if err != nil {
				fmt.Printf("Warning: %v\n", err)
				ok = false
				break
			}
			tmp, err := createTempDir("ub_nest_ext_")
			if err != nil {
				fmt.Printf("Warning: failed to create temp dir: %v\n", err)
				ok = false
				break
			}
			defer os.RemoveAll(tmp)
			if err := extractArchive(nestedPath, tmp); err != nil {
				fmt.Printf("Warning: failed to read nested archive %s: %v\n", archiveRel, err)
				ok = false
				break
			}
			cur = tmp
		}
		if !ok {
			continue
		}

		sourceFile, err := safeJoin(cur, loc.ItemPath)
		if err != nil {
			fmt.Printf("Warning: %v\n", err)
			continue
		}

		// 嵌套文件放到以嵌套归档名命名的目录下，避免拍平后同名互相覆盖
		destRoot := "."
		if len(loc.Chain) > 0 {
			destRoot = stripArchiveExt(filepath.Base(loc.Chain[len(loc.Chain)-1]))
		}
		destFile, err := safeJoin(destRoot, loc.ItemPath)
		if err != nil {
			fmt.Printf("Warning: %v\n", err)
			continue
		}

		if _, err := os.Stat(destFile); err == nil {
			if !promptYN(fmt.Sprintf("File '%s' already exists, overwrite?", destFile)) {
				fmt.Printf("Skipped: %s\n", destFile)
				continue
			}
		}

		if err := os.MkdirAll(filepath.Dir(destFile), 0755); err != nil {
			fmt.Printf("Warning: failed to create directory: %v\n", err)
			continue
		}
		if err := copyFile(sourceFile, destFile); err != nil {
			fmt.Printf("Warning: failed to extract %s: %v\n", loc.ItemPath, err)
		} else {
			fmt.Printf("Extracted: %s\n", destFile)
		}
	}

	fmt.Println("Extract operation completed")
	return nil
}

// ============== 通用逻辑 ==============
func addFilesToArchive(archive string, filesToAdd []string) error {
	if err := canWriteFormat(archive); err != nil {
		return err
	}

	absArchive, err := filepath.Abs(archive)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path for archive: %v", err)
	}

	var files []string
	for _, file := range filesToAdd {
		absFile, _ := filepath.Abs(file)
		if absFile == absArchive {
			fmt.Fprintf(os.Stderr, "Warning: cannot add archive '%s' to itself, skipping\n", file)
			continue
		}
		fi, err := os.Stat(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: file '%s' does not exist, skipping\n", file)
			continue
		}
		if fi.IsDir() {
			fmt.Fprintf(os.Stderr, "Warning: '%s' is a directory, only files can be added, skipping\n", file)
			continue
		}
		files = append(files, file)
	}

	if len(files) == 0 {
		fmt.Println("No new files were added. Archive remains unchanged.")
		return nil
	}

	// zip/7z 走原生添加快路径（同名条目直接更新）
	lower := strings.ToLower(archive)
	if (strings.HasSuffix(lower, ".zip") && commandExists("zip")) ||
		(strings.HasSuffix(lower, ".7z") && commandExists("7z")) {
		return nativeAdd(archive, files)
	}

	// tar 家族：全量解压 → 复制 → 原子重压
	tmpdir, err := createTempDir("ub_add_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpdir)

	if err := extractArchive(archive, tmpdir); err != nil {
		return fmt.Errorf("extraction failed, cannot add files: %v", err)
	}

	addedCount := 0
	for _, file := range files {
		dst := filepath.Join(tmpdir, filepath.Base(file))
		if _, err := os.Stat(dst); err == nil {
			if !promptYN(fmt.Sprintf("File '%s' already exists in archive, overwrite?", filepath.Base(file))) {
				fmt.Printf("Skipped: %s\n", file)
				continue
			}
		}
		if err := copyFile(file, dst); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to copy %s: %v\n", file, err)
			continue
		}
		fmt.Printf("Copied: %s\n", file)
		addedCount++
	}

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

// nativeAdd 用系统工具把文件原地添加/更新进 zip/7z 归档（条目存为根级 basename）
func nativeAdd(archive string, files []string) error {
	tmpPath, err := makeArchiveTemp(archive)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)

	absArchive, err := filepath.Abs(archive)
	if err != nil {
		return err
	}
	if err := copyFile(absArchive, tmpPath); err != nil {
		return err
	}

	// 按父目录分组，保证条目以 basename 存入归档根级
	byDir := make(map[string][]string)
	var dirs []string
	for _, file := range files {
		dir := filepath.Dir(file)
		if _, exists := byDir[dir]; !exists {
			dirs = append(dirs, dir)
		}
		byDir[dir] = append(byDir[dir], filepath.Base(file))
	}

	isZip := strings.HasSuffix(strings.ToLower(archive), ".zip")
	for _, dir := range dirs {
		var runErr error
		if isZip {
			args := append([]string{"-rq", tmpPath}, byDir[dir]...)
			runErr = runCommandInDirArgs(dir, "zip", args)
		} else {
			args := append([]string{"a", "-t7z", tmpPath}, byDir[dir]...)
			runErr = runCommandInDirArgs(dir, "7z", args)
		}
		if runErr != nil {
			return runErr
		}
	}

	if err := os.Rename(tmpPath, absArchive); err != nil {
		return fmt.Errorf("failed to replace archive: %v", err)
	}
	for _, file := range files {
		fmt.Printf("Added/Updated: %s\n", file)
	}
	return nil
}

func compressArchive(archive, sourceDir string) error {
	if err := canWriteFormat(archive); err != nil {
		return err
	}

	absArchive, err := filepath.Abs(archive)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %v", err)
	}

	// 先写同目录临时文件，成功后再原子重命名覆盖原归档，失败时原归档完好
	tmpPath, err := makeArchiveTemp(archive)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpPath)

	// 枚举顶层条目作为压缩参数，避免归档内出现 "./" 前缀条目（影响原生删除的精确匹配）
	names, err := topLevelNames(sourceDir)
	if err != nil {
		return fmt.Errorf("failed to read source directory: %v", err)
	}

	var runErr error
	switch {
	case strings.HasSuffix(archive, ".zip"):
		args := append([]string{"-rq", tmpPath}, names...)
		runErr = runCommandInDirArgs(sourceDir, "zip", args)
	case strings.HasSuffix(archive, ".tar"):
		args := append([]string{"cf", tmpPath, "-C", sourceDir}, names...)
		runErr = runCommandInDirArgs("", "tar", args)
	case strings.HasSuffix(archive, ".tar.gz") || strings.HasSuffix(archive, ".tgz"):
		args := append([]string{"czf", tmpPath, "-C", sourceDir}, names...)
		runErr = runCommandInDirArgs("", "tar", args)
	case strings.HasSuffix(archive, ".tar.bz2") || strings.HasSuffix(archive, ".tbz2"):
		args := append([]string{"cjf", tmpPath, "-C", sourceDir}, names...)
		runErr = runCommandInDirArgs("", "tar", args)
	case strings.HasSuffix(archive, ".tar.xz") || strings.HasSuffix(archive, ".txz"):
		args := append([]string{"cJf", tmpPath, "-C", sourceDir}, names...)
		runErr = runCommandInDirArgs("", "tar", args)
	case strings.HasSuffix(archive, ".tar.zst") || strings.HasSuffix(archive, ".tzst"):
		args := append([]string{"--zstd", "-cf", tmpPath, "-C", sourceDir}, names...)
		runErr = runCommandInDirArgs("", "tar", args)
	case strings.HasSuffix(archive, ".7z"):
		args := append([]string{"a", tmpPath}, names...)
		runErr = runCommandInDirArgs(sourceDir, "7z", args)
	default:
		runErr = fmt.Errorf("unsupported format for modifying: %s", archive)
	}

	if runErr != nil {
		return runErr
	}
	if err := os.Rename(tmpPath, absArchive); err != nil {
		return fmt.Errorf("failed to replace archive: %v", err)
	}
	return nil
}

func stripArchiveExt(filename string) string {
	base := filepath.Base(filename)
	lower := strings.ToLower(base)
	compoundExts := []string{".tar.bz2", ".tar.gz", ".tar.xz", ".tar.zst"}
	for _, ext := range compoundExts {
		if strings.HasSuffix(lower, ext) {
			return base[:len(base)-len(ext)]
		}
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func processFile(file string, config *Config) error {
	if _, err := os.Stat(file); err != nil {
		return fmt.Errorf("'%s' is not a valid file", file)
	}

	dest := stripArchiveExt(file)
	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("failed to create directory '%s': %v", dest, err)
	}

	fmt.Printf("Extracting: %s -> %s/\n", file, dest)
	if err := extractArchive(file, dest); err != nil {
		return err
	}

	// Simple interactive deletion for full extraction mode
	if config.deleteOrigin && promptYN(fmt.Sprintf("Delete original archive %s?", file)) {
		os.Remove(file)
		fmt.Printf("Removed: %s\n", file)
	}

	return nil
}
