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

type Config struct {
	deleteOrigin  bool
	listContent   bool
	addFiles      []string
	deleteContent bool
	contentMap    map[int]string
	currentNumber int
}

func main() {
	config := &Config{
		contentMap:    make(map[int]string),
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

	// Handle delete content mode
	if config.deleteContent {
		if len(files) != 1 || len(config.addFiles) > 0 || config.deleteOrigin || config.listContent {
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

	// Handle add files mode
	if len(config.addFiles) > 0 {
		if len(files) != 1 {
			fmt.Fprintln(os.Stderr, "Error: Add files mode requires exactly one archive file")
			os.Exit(1)
		}
		if config.deleteOrigin || config.listContent {
			fmt.Fprintln(os.Stderr, "Error: Add files mode cannot be used with -o or -l options")
			os.Exit(1)
		}
		if err := addFilesToArchive(files[0], config.addFiles); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Process all files
	for _, file := range files {
		fmt.Println("==================================")
		if err := processFile(file, config); err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %v\n", file, err)
		}
		fmt.Println("==================================")
	}
}

func showHelp() {
    ansiReset := "\033[0m"
    fmt.Printf(`
%s[96mOptions:%s
    %s[32m-o%s      Delete original archive after successful extraction.
    %s[32m-l%s      Show the file of the archived.
    %s[32m-a%s      Add files to the archived.
    %s[32m-d%s      Delete file form the archive.
    %s[32m-h%s      Display this help message.
    %s[32m-v%s      Display version and license message.

%s[96mExamples:%s
    %s[93munbox archive.zip backup.tar%s
    %s[93munbox -l archive.zip%s
    %s[93munbox -a file archive.zip%s
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
		case "-l":
			config.listContent = true
		case "-d":
			config.deleteContent = true
		case "-h":
			showHelp()
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

	switch {
	case strings.HasSuffix(file, ".tar.bz2") || strings.HasSuffix(file, ".tbz2"):
		return runCommand("tar", "xjf", file, "-C", dest)
	case strings.HasSuffix(file, ".tar.gz") || strings.HasSuffix(file, ".tgz"):
		return runCommand("tar", "xzf", file, "-C", dest)
	case strings.HasSuffix(file, ".tar.xz") || strings.HasSuffix(file, ".txz"):
		return runCommand("tar", "xJf", file, "-C", dest)
	case strings.HasSuffix(file, ".bz2"):
		if dest != "." {
			if err := copyFile(file, filepath.Join(dest, filepath.Base(file))); err != nil {
				return err
			}
			return runCommandInDir(dest, "bunzip2", filepath.Base(file))
		}
		return runCommand("bunzip2", file)
	case strings.HasSuffix(file, ".rar"):
		if !commandExists("unrar") {
			return fmt.Errorf("unrar is required to extract .rar files")
		}
		return runCommand("unrar", "x", file, dest)
	case strings.HasSuffix(file, ".gz"):
		if dest != "." {
			if err := copyFile(file, filepath.Join(dest, filepath.Base(file))); err != nil {
				return err
			}
			return runCommandInDir(dest, "gunzip", filepath.Base(file))
		}
		return runCommand("gunzip", file)
	case strings.HasSuffix(file, ".tar"):
		return runCommand("tar", "xf", file, "-C", dest)
	case strings.HasSuffix(file, ".zip"):
		if dest != "." {
			return runCommand("unzip", "-q", file, "-d", dest)
		}
		return runCommand("unzip", file)
	case strings.HasSuffix(file, ".Z"):
		if dest != "." {
			if err := copyFile(file, filepath.Join(dest, filepath.Base(file))); err != nil {
				return err
			}
			return runCommandInDir(dest, "uncompress", filepath.Base(file))
		}
		return runCommand("uncompress", file)
	case strings.HasSuffix(file, ".7z"):
		return runCommand("7z", "x", file, "-o"+dest)
	case strings.HasSuffix(file, ".xz"):
		if dest != "." {
			if err := copyFile(file, filepath.Join(dest, filepath.Base(file))); err != nil {
				return err
			}
			return runCommandInDir(dest, "unxz", filepath.Base(file))
		}
		return runCommand("unxz", file)
	case strings.HasSuffix(file, ".lzma"):
		if dest != "." {
			if err := copyFile(file, filepath.Join(dest, filepath.Base(file))); err != nil {
				return err
			}
			return runCommandInDir(dest, "unlzma", filepath.Base(file))
		}
		return runCommand("unlzma", file)
	default:
		return fmt.Errorf("unsupported format: %s", file)
	}
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runCommandInDir(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
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
	return os.MkdirTemp("/tmp", prefix)
}

func addFilesToArchive(archive string, filesToAdd []string) error {
	tmpdir, err := createTempDir("ub_add_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpdir)

	if err := extractArchive(archive, tmpdir); err != nil {
		return fmt.Errorf("extraction failed, cannot add files: %v", err)
	}

	for _, file := range filesToAdd {
		if _, err := os.Stat(file); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: file '%s' does not exist, skipping\n", file)
			continue
		}
		dst := filepath.Join(tmpdir, filepath.Base(file))
		if err := copyFile(file, dst); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to copy '%s': %v\n", file, err)
		} else {
			fmt.Printf("Copied: %s\n", file)
		}
	}

	fmt.Printf("Recompressing to: %s\n", archive)
	if err := compressArchive(archive, tmpdir); err != nil {
		return err
	}

	fmt.Println("Files added successfully")
	return nil
}

func compressArchive(archive, sourceDir string) error {
	switch {
	case strings.HasSuffix(archive, ".zip"):
		return runCommand("zip", "-jr", archive, sourceDir+"/*")
	case strings.HasSuffix(archive, ".tar"):
		return runCommand("tar", "cf", archive, "-C", sourceDir, ".")
	case strings.HasSuffix(archive, ".tar.gz") || strings.HasSuffix(archive, ".tgz"):
		return runCommand("tar", "czf", archive, "-C", sourceDir, ".")
	case strings.HasSuffix(archive, ".tar.bz2") || strings.HasSuffix(archive, ".tbz2"):
		return runCommand("tar", "cjf", archive, "-C", sourceDir, ".")
	case strings.HasSuffix(archive, ".tar.xz") || strings.HasSuffix(archive, ".txz"):
		return runCommand("tar", "cJf", archive, "-C", sourceDir, ".")
	case strings.HasSuffix(archive, ".7z"):
		return runCommand("7z", "a", archive, sourceDir+"/*")
	default:
		return fmt.Errorf("unsupported format for adding files: %s", archive)
	}
}

func listDir(dir, prefix string, isLast bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	// Sort entries
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	count := len(entries)
	for idx, entry := range entries {
		isLastItem := (idx == count-1)
		fullPath := filepath.Join(dir, entry.Name())

		// Print prefix
		if idx == 0 && prefix != "" {
			fmt.Print(prefix)
			if isLast {
				fmt.Print("    ")
			} else {
				fmt.Print("│   ")
			}
		}

		if idx > 0 {
			fmt.Print(prefix)
			if isLast {
				fmt.Print("    ")
			} else {
				fmt.Print("│   ")
			}
		}

		// Print tree structure
		if isLastItem {
			fmt.Print("└── ")
		} else {
			fmt.Print("├── ")
		}

		// Check if it's a compressed file
		if isCompressedFile(fullPath) {
			nestedCount := getNestedFileCount(fullPath)
			if nestedCount > 0 {
				fmt.Printf("%s [嵌套压缩包, 包含 %d 个文件]\n", entry.Name(), nestedCount)
			} else {
				fmt.Printf("%s [嵌套压缩包]\n", entry.Name())
			}
		} else if entry.IsDir() {
			fmt.Printf("%s/\n", entry.Name())
			// Recursive call for subdirectories
			newPrefix := prefix
			if isLast {
				newPrefix += "    "
			} else {
				newPrefix += "│   "
			}
			listDir(fullPath, newPrefix, isLastItem)
		} else {
			fmt.Printf("%s\n", entry.Name())
		}
	}

	return nil
}

func getNestedFileCount(archive string) int {
	switch {
	case strings.HasSuffix(archive, ".zip"):
		cmd := exec.Command("unzip", "-l", archive)
		output, err := cmd.Output()
		if err != nil {
			return 0
		}
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			if strings.Contains(line, "files") {
				fields := strings.Fields(line)
				if len(fields) > 0 {
					if count, err := strconv.Atoi(fields[0]); err == nil {
						return count
					}
				}
			}
		}
	case strings.HasSuffix(archive, ".tar") || strings.Contains(archive, ".tar."):
		cmd := exec.Command("tar", "tf", archive)
		output, err := cmd.Output()
		if err != nil {
			return 0
		}
		return len(strings.Split(strings.TrimSpace(string(output)), "\n"))
	case strings.HasSuffix(archive, ".7z"):
		cmd := exec.Command("7z", "l", archive)
		output, err := cmd.Output()
		if err != nil {
			return 0
		}
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			if strings.Contains(line, "files") {
				fields := strings.Fields(line)
				if len(fields) > 0 {
					if count, err := strconv.Atoi(fields[0]); err == nil {
						return count
					}
				}
			}
		}
	}
	return 0
}

func listArchiveContents(archive, prefix string, config *Config) error {
	tmpdir, err := createTempDir("ub_list_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpdir)

	if err := extractArchive(archive, tmpdir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot extract '%s', skipping\n", archive)
		return err
	}

	entries, err := os.ReadDir(tmpdir)
	if err != nil {
		return err
	}

	if len(entries) == 0 {
		return nil
	}

	// Sort entries
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	numItems := len(entries)
	for i, entry := range entries {
		itemName := entry.Name()
		relPath := itemName

		var linePrefix, newPrefix string
		if i == numItems-1 {
			linePrefix = prefix + "└── "
			newPrefix = prefix + "    "
		} else {
			linePrefix = prefix + "├── "
			newPrefix = prefix + "│   "
		}

		config.contentMap[config.currentNumber] = archive + ":" + relPath
		fmt.Printf("%s%d) %s\n", linePrefix, config.currentNumber, itemName)

		fullPath := filepath.Join(tmpdir, itemName)
		if isCompressedFile(fullPath) {
			config.currentNumber++
			listArchiveContents(fullPath, newPrefix, config)
		} else {
			config.currentNumber++
		}
	}

	return nil
}

func deleteFilesFromArchive(mainArchive string, filesToDelete []string) error {
	mainTmpdir, err := createTempDir("ub_delete_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mainTmpdir)

	fmt.Printf("Using temporary directory: %s\n", mainTmpdir)

	if err := extractArchive(mainArchive, mainTmpdir); err != nil {
		return fmt.Errorf("failed to extract main archive: %v", err)
	}

	for _, entry := range filesToDelete {
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 {
			continue
		}
		archive := parts[0]
		relPath := parts[1]

		var nestedArchiveInMain string
		if archive != mainArchive {
			// Find nested archive
			err := filepath.Walk(mainTmpdir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if filepath.Base(path) == filepath.Base(archive) {
					nestedArchiveInMain = path
					return filepath.SkipDir
				}
				return nil
			})
			if err != nil || nestedArchiveInMain == "" {
				fmt.Fprintf(os.Stderr, "Warning: nested archive '%s' not found, skipping deletion of '%s'\n", archive, relPath)
				continue
			}
		} else {
			nestedArchiveInMain = filepath.Join(mainTmpdir, relPath)
		}

		if archive != mainArchive {
			nestedTmpdir, err := createTempDir("ub_nested_delete_")
			if err != nil {
				continue
			}

			if err := extractArchive(nestedArchiveInMain, nestedTmpdir); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: cannot extract nested archive '%s', skipping\n", nestedArchiveInMain)
				os.RemoveAll(nestedTmpdir)
				continue
			}

			fileToDeleteInNested := filepath.Join(nestedTmpdir, relPath)
			if _, err := os.Stat(fileToDeleteInNested); err == nil {
				os.RemoveAll(fileToDeleteInNested)
				fmt.Printf("Deleted nested file: %s\n", relPath)
				os.Remove(nestedArchiveInMain)
				compressArchive(nestedArchiveInMain, nestedTmpdir)
			} else {
				fmt.Fprintf(os.Stderr, "Warning: nested file '%s' does not exist, skipping\n", relPath)
			}
			os.RemoveAll(nestedTmpdir)
		} else {
			fileToDelete := filepath.Join(mainTmpdir, relPath)
			if _, err := os.Stat(fileToDelete); err == nil {
				os.RemoveAll(fileToDelete)
				fmt.Printf("Deleted file: %s\n", relPath)
			} else {
				fmt.Fprintf(os.Stderr, "Warning: file '%s' does not exist, skipping\n", relPath)
			}
		}
	}

	fmt.Printf("Recompressing main archive: %s\n", mainArchive)
	os.Remove(mainArchive)
	if err := compressArchive(mainArchive, mainTmpdir); err != nil {
		return err
	}
	fmt.Println("Delete operation completed")
	return nil
}

func processDelete(archive string, config *Config) error {
	fmt.Println("Listing archive contents:")

	config.contentMap = make(map[int]string)
	config.currentNumber = 1
	if err := listArchiveContents(archive, "", config); err != nil {
		return err
	}

	if len(config.contentMap) == 0 {
		fmt.Println("Archive is empty, no content to delete")
		return nil
	}

	fmt.Print("Enter the number: ")
	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return err
	}

	input = strings.TrimSpace(input)
	numbers := strings.Fields(input)
	var filesToDelete []string

	for _, numStr := range numbers {
		num, err := strconv.Atoi(numStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: '%s' is not a valid number, skipping\n", numStr)
			continue
		}
		if entry, exists := config.contentMap[num]; exists {
			filesToDelete = append(filesToDelete, entry)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: number '%d' does not exist, skipping\n", num)
		}
	}

	if len(filesToDelete) == 0 {
		fmt.Println("No valid files to delete")
		return nil
	}

	return deleteFilesFromArchive(archive, filesToDelete)
}

func findCompressedFiles(dir string) ([]string, error) {
	var foundFiles []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && isCompressedFile(path) {
			foundFiles = append(foundFiles, path)
		}
		return nil
	})
	return foundFiles, err
}

func handleNestedArchives(dir string, deleteFlag string, listFlag bool) error {
	nestedFiles, err := findCompressedFiles(dir)
	if err != nil {
		return err
	}

	if len(nestedFiles) == 0 {
		return nil
	}

	reader := bufio.NewReader(os.Stdin)
	for _, file := range nestedFiles {
//		fmt.Printf("是否预览嵌套压缩包? %s \033[32m(y)es\033[0m/\033[31m(n)o\033[0m: ", file)
		fmt.Printf("是否预览嵌套压缩包? %s (\033[32my\033[0m)es/(\033[31mn\033[0m)o: ", file)
		answer, err := reader.ReadString('\n')
		if err != nil {
			continue
		}
		answer = strings.TrimSpace(strings.ToLower(answer))

		if answer == "y" || answer == "yes" {
			if listFlag {
				// Preview mode
				tmpdir, err := createTempDir("ub_nested_")
				if err != nil {
					continue
				}

				if err := copyFile(file, filepath.Join(tmpdir, filepath.Base(file))); err != nil {
					os.RemoveAll(tmpdir)
					continue
				}

				basefile := filepath.Base(file)
				if err := extractArchive(filepath.Join(tmpdir, basefile), tmpdir); err != nil {
					os.RemoveAll(tmpdir)
					continue
				}

				os.Remove(filepath.Join(tmpdir, basefile))

				fmt.Println("\n嵌套压缩包内容:")
				listDir(tmpdir, "", false)

				handleNestedArchives(tmpdir, "false", true)

				if deleteFlag == "ask" {
//					fmt.Printf("是否删除嵌套压缩包? %s \033[32m(y)es\033[0m/\033[31m(n)o\033[0m: ", file)
					fmt.Printf("是否删除嵌套压缩包? %s (\033[32my\033[0m)es/(\033[31mn\033[0m)o: ", file)
					delAnswer, err := reader.ReadString('\n')
					if err == nil {
						delAnswer = strings.TrimSpace(strings.ToLower(delAnswer))
						if delAnswer == "y" || delAnswer == "yes" {
							os.Remove(file)
							fmt.Printf("Removed: %s\n", file)
						}
					}
				} else if deleteFlag == "true" {
					os.Remove(file)
					fmt.Printf("Removed: %s\n", file)
				}

				os.RemoveAll(tmpdir)
			} else {
				// Normal mode
				if err := extractArchive(file, "."); err != nil {
					continue
				}

				if deleteFlag == "ask" {
//					fmt.Printf("是否删除嵌套压缩包? %s \033[32m(y)es\033[0m/\033[31m(n)o\033[0m: ", file)
					fmt.Printf("是否删除嵌套压缩包? %s (\033[32my\033[0m)es/(\033[31mn\033[0m)o: ", file)
					delAnswer, err := reader.ReadString('\n')
					if err == nil {
						delAnswer = strings.TrimSpace(strings.ToLower(delAnswer))
						if delAnswer == "y" || delAnswer == "yes" {
							os.Remove(file)
							fmt.Printf("Removed: %s\n", file)
						}
					}
				} else if deleteFlag == "true" {
					os.Remove(file)
					fmt.Printf("Removed: %s\n", file)
				}
			}
		}
	}

	return nil
}

func listInTemp(file string, config *Config) error {
	tmpdir, err := createTempDir("ub_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpdir)

	if err := copyFile(file, filepath.Join(tmpdir, filepath.Base(file))); err != nil {
		return err
	}

	basefile := filepath.Base(file)
	if err := extractArchive(filepath.Join(tmpdir, basefile), tmpdir); err != nil {
		return err
	}

	os.Remove(filepath.Join(tmpdir, basefile))

	deleteFlag := "false"
	if config.deleteOrigin {
		deleteFlag = "ask"
	}

	if err := handleNestedArchives(tmpdir, deleteFlag, true); err != nil {
		return err
	}

	fmt.Printf("\n%s\n", file)
	return listDir(tmpdir, "", false)
}

func processFile(file string, config *Config) error {
	if _, err := os.Stat(file); err != nil {
		return fmt.Errorf("'%s' is not a valid file", file)
	}

	// Handle list mode
	if config.listContent {
		return listInTemp(file, config)
	}

	// Normal extraction mode
	fmt.Printf("Extracting: %s\n", file)
	if err := extractArchive(file, "."); err != nil {
		return err
	}

	deleteFlag := "false"
	if config.deleteOrigin {
		deleteFlag = "ask"
	}

	if err := handleNestedArchives(".", deleteFlag, false); err != nil {
		return err
	}

	if config.deleteOrigin {
		os.Remove(file)
		fmt.Printf("Removed: %s\n", file)
	}

	return nil
}
