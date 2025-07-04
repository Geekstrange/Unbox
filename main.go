package main

import (
        "bufio"
        "bytes"
        "errors"
        "flag"
        "fmt"
        "io"
        "io/ioutil"
        "log"
        "net/url"
        "os"
        "os/exec"
        "path/filepath"
        "regexp"
        "strings"
        "syscall"
)

// 版本信息
const (
        VERSION = "0.0.1"
)

const (
        LICENSE_BANNER = `unbox version %s
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
)

// 内容类型常量
const (
        MATCHING_DIRECTORY = iota + 1
        ONE_ENTRY_KNOWN
        BOMB
        EMPTY
)

// 单条目策略常量
const (
        EXTRACT_HERE = iota + 1
        EXTRACT_WRAP
        EXTRACT_RENAME
)

// 递归策略常量
const (
        RECURSE_ALWAYS = iota + 1
        RECURSE_ONCE
        RECURSE_NOT_NOW
        RECURSE_NEVER
        RECURSE_LIST
)

// 配置选项
type Options struct {
        ShowList        bool
        Metadata        bool
        Recursive       bool
        OneEntryDefault string
        Batch           bool
        Overwrite       bool
        Flat            bool
        LogLevel        int
        OneEntryPolicy  OneEntryPolicy
        RecursionPolicy RecursionPolicy
        Filenames       []string
}

// 单条目策略
type OneEntryPolicy struct {
        CurrentPolicy int
}

// 递归策略
type RecursionPolicy struct {
        CurrentPolicy int
}

// 文件检查器
type FilenameChecker struct {
        OriginalName string
}

func (fc *FilenameChecker) IsFree(filename string) bool {
        _, err := os.Stat(filename)
        return os.IsNotExist(err)
}

func (fc *FilenameChecker) Create() (string, error) {
        return ioutil.TempDir(".", fc.OriginalName+".")
}

func (fc *FilenameChecker) Check() (string, error) {
        for i := 0; i < 10; i++ {
                suffix := ""
                if i > 0 {
                        suffix = fmt.Sprintf(".%d", i)
                }
                filename := fc.OriginalName + suffix
                if fc.IsFree(filename) {
                        return filename, nil
                }
        }
        return fc.Create()
}

// 目录检查器
type DirectoryChecker struct {
        FilenameChecker
}

func (dc *DirectoryChecker) Create() (string, error) {
        return ioutil.TempDir(".", dc.OriginalName+".")
}

// 提取器接口
type Extractor interface {
        Extract(ignorePasswd bool) error
        GetFilenames() ([]string, error)
        CheckContents() error
        CheckSuccess(gotFiles bool) error
        GetBase() *BaseExtractor
}

// 基础提取器
type BaseExtractor struct {
        Filename         string
        Encoding         string
        IgnorePw         bool
        FileCount        int
        IncludedArchives []string
        IncludedRoot     string
        Target           string
        ContentType      int
        ContentName      string
        Contents         []string
        Pipes            [][]string
        UserStdin        bool
        Stderr           string
        PwPrompted       bool
        ExitCodes        []int
        Archive          *os.File
        ExtractPipe      []string
        ListPipe         []string
}

func NewBaseExtractor(filename, encoding string) (*BaseExtractor, error) {
        archive, err := os.Open(filename)
        if err != nil {
                return nil, fmt.Errorf("could not open %s: %v", filename, err)
        }

        extractor := &BaseExtractor{
                Filename: filename,
                Encoding: encoding,
                Archive:  archive,
        }

        // 处理编码
        if encoding != "" {
                decoders := map[string][]string{
                        "bzip2":   {"bzcat"},
                        "gzip":    {"zcat"},
                        "compress": {"zcat"},
                        "lzma":    {"lzcat"},
                        "xz":      {"xzcat"},
                        "lzip":    {"lzip", "-cd"},
                        "zstd":    {"zstd", "-d"},
                        "br":      {"br", "--decompress"},
                }

                if decoder, ok := decoders[encoding]; ok {
                        extractor.Pipes = append(extractor.Pipes, decoder)
                } else {
                        return nil, fmt.Errorf("unrecognized encoding %s", encoding)
                }
        }

        return extractor, nil
}

func (e *BaseExtractor) Extract(ignorePasswd bool) error {
        e.IgnorePw = ignorePasswd

        // 创建临时目录
        target, err := ioutil.TempDir(".", ".unbox-")
        if err != nil {
                return fmt.Errorf("cannot extract here: %v", err)
        }
        e.Target = target

        oldPath, err := os.Getwd()
        if err != nil {
                return err
        }

        // 切换到临时目录
        if err := os.Chdir(e.Target); err != nil {
                return err
        }

        defer func() {
                // 恢复工作目录
                os.Chdir(oldPath)
                // 如果出错，清理临时目录
                if r := recover(); r != nil {
                        os.RemoveAll(e.Target)
                        panic(r)
                }
        }()

        // 重置读取位置
        if _, err := e.Archive.Seek(0, 0); err != nil {
                return err
        }

        // 执行提取
        if err := e.extractArchive(); err != nil {
                return err
        }

        // 检查内容
        contents, err := ioutil.ReadDir(".")
        if err != nil {
                return err
        }

        e.Contents = make([]string, len(contents))
        for i, item := range contents {
                e.Contents[i] = item.Name()
        }

        if err := e.CheckContents(); err != nil {
                return err
        }

        // 检查提取是否成功
        gotFiles := e.ContentType != EMPTY
        if err := e.CheckSuccess(gotFiles); err != nil {
                return err
        }

        return nil
}

func (e *BaseExtractor) extractArchive() error {
        // 添加提取命令
        e.Pipes = append(e.Pipes, e.ExtractPipe)

        // 执行管道命令
        return e.runPipes(nil)
}

func (e *BaseExtractor) GetFilenames() ([]string, error) {
        // 添加列出内容的命令
        e.Pipes = append(e.Pipes, e.ListPipe)

        var filenames []string

        // 创建一个内存缓冲区来捕获输出
        var output bytes.Buffer

        // 执行管道命令，将输出导向缓冲区
        if err := e.runPipes(&output); err != nil {
                return nil, err
        }

        // 解析输出
        scanner := bufio.NewScanner(&output)
        for scanner.Scan() {
                filenames = append(filenames, scanner.Text())
        }

        if err := scanner.Err(); err != nil {
                return nil, err
        }

        return filenames, nil
}

func (e *BaseExtractor) CheckContents() error {
        if len(e.Contents) == 0 {
                e.ContentType = EMPTY
        } else if len(e.Contents) == 1 {
                baseName := e.basename()
                firstContent := e.Contents[0]

                if baseName == firstContent {
                        e.ContentType = MATCHING_DIRECTORY
                } else {
                        info, err := os.Stat(firstContent)
                        if err != nil {
                                return err
                        }

                        if info.IsDir() {
                                e.ContentType = ONE_ENTRY_KNOWN
                                e.ContentName = firstContent + "/"
                        } else {
                                e.ContentType = ONE_ENTRY_KNOWN
                                e.ContentName = firstContent
                        }
                }
        } else {
                e.ContentType = BOMB
        }

        // 检查包含的归档文件
        return e.checkIncludedArchives()
}

func (e *BaseExtractor) checkIncludedArchives() error {
        var includedRoot string
        if e.ContentName == "" || !strings.HasSuffix(e.ContentName, "/") {
                includedRoot = "./"
        } else {
                includedRoot = e.ContentName
        }

        startIndex := len(includedRoot)

        err := filepath.Walk(includedRoot, func(path string, info os.FileInfo, err error) error {
                if err != nil {
                        return err
                }

                if !info.IsDir() {
                        e.FileCount++

                        relPath := path[startIndex:]
                        if isArchiveFile(relPath) {
                                e.IncludedArchives = append(e.IncludedArchives, relPath)
                        }
                }

                return nil
        })

        return err
}

func (e *BaseExtractor) basename() string {
        base := filepath.Base(e.Filename)
        parts := strings.Split(base, ".")

        origLen := len(parts)

        // 处理编码扩展名
        if origLen > 1 {
                ext := "." + parts[len(parts)-1]
                if _, ok := mimetypesEncodingsMap[ext]; ok {
                        parts = parts[:len(parts)-1]

                        if len(parts) > 1 {
                                ext = "." + parts[len(parts)-1]
                                if _, ok := mimetypesTypesMap[ext]; ok {
                                        parts = parts[:len(parts)-1]
                                } else if _, ok := mimetypesCommonTypes[ext]; ok {
                                        parts = parts[:len(parts)-1]
                                }
                        }
                }
        }

        // 如果没有扩展名或者扩展名很短，则不处理
        if len(parts) == origLen && origLen > 1 && len(parts[len(parts)-1]) < 5 {
                parts = parts[:len(parts)-1]
        }

        return strings.Join(parts, ".")
}

func (e *BaseExtractor) CheckSuccess(gotFiles bool) error {
        for i, code := range e.ExitCodes {
                if code > 0 {
                        if e.isFatalError(code) || (!gotFiles && code != 0) {
                                command := strings.Join(e.Pipes[i], " ")
                                return fmt.Errorf("%s error: '%s' returned status code %d",
                                        "extraction", command, code)
                        }
                }
        }

        return nil
}

func (e *BaseExtractor) isFatalError(status int) bool {
        return status > 1
}

func (e *BaseExtractor) runPipes(output io.Writer) error {
        if len(e.Pipes) == 0 {
                return nil
        }

        // 处理最后一个输出目标
        if output == nil {
                outputFile, err := os.Create(os.DevNull)
                if err != nil {
                        return err
                }
                defer outputFile.Close()
                output = outputFile
        }

        // 创建管道链
        numPipes := len(e.Pipes)
        processes := make([]*exec.Cmd, numPipes)
        pipeReaders := make([]io.ReadCloser, numPipes-1)

        var err error

        // 创建所有管道
        for i := 0; i < numPipes-1; i++ {
                var pipeWriter io.WriteCloser
                pipeReaders[i], pipeWriter, err = os.Pipe()
                if err != nil {
                        return err
                }
                defer pipeWriter.Close()

                // 创建命令
                processes[i] = exec.Command(e.Pipes[i][0], e.Pipes[i][1:]...)
                if i == 0 {
                        if e.UserStdin {
                                processes[i].Stdin = os.Stdin
                        } else {
                                processes[i].Stdin = e.Archive
                        }
                } else {
                        processes[i].Stdin = pipeReaders[i-1]
                }

                processes[i].Stdout = pipeWriter
                processes[i].Stderr = os.Stderr

                // 启动命令
                if err := processes[i].Start(); err != nil {
                        return err
                }

                // 关闭写入端，因为已经传递给命令
                pipeWriter.Close()
        }

        // 处理最后一个命令
        lastIndex := numPipes - 1
        processes[lastIndex] = exec.Command(e.Pipes[lastIndex][0], e.Pipes[lastIndex][1:]...)
        if lastIndex > 0 {
                processes[lastIndex].Stdin = pipeReaders[lastIndex-1]
        } else {
                if e.UserStdin {
                        processes[lastIndex].Stdin = os.Stdin
                } else {
                        processes[lastIndex].Stdin = e.Archive
                }
        }

        processes[lastIndex].Stdout = output
        processes[lastIndex].Stderr = os.Stderr

        // 启动最后一个命令
        if err := processes[lastIndex].Start(); err != nil {
                return err
        }

        // 等待所有命令完成
        e.ExitCodes = make([]int, numPipes)
        for i, proc := range processes {
                if err := proc.Wait(); err != nil {
                        if exiterr, ok := err.(*exec.ExitError); ok {
                                if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
                                        e.ExitCodes[i] = status.ExitStatus()
                                }
                        } else {
                                return err
                        }
                } else {
                        e.ExitCodes[i] = 0
                }
        }

        // 关闭所有读取端
        for _, reader := range pipeReaders {
                if reader != nil {
                        reader.Close()
                }
        }

        // 关闭源文件
        e.Archive.Close()

        return nil
}

func (e *BaseExtractor) GetBase() *BaseExtractor {
        return e
}

// 压缩文件提取器
type CompressionExtractor struct {
        BaseExtractor
}

func NewCompressionExtractor(filename, encoding string) (*CompressionExtractor, error) {
        base, err := NewBaseExtractor(filename, encoding)
        if err != nil {
                return nil, err
        }

        extractor := &CompressionExtractor{
                BaseExtractor: *base,
        }

        return extractor, nil
}

func (e *CompressionExtractor) basename() string {
        base := filepath.Base(e.Filename)
        parts := strings.Split(base, ".")

        if len(parts) > 1 {
                ext := "." + parts[len(parts)-1]
                if _, ok := mimetypesEncodingsMap[ext]; ok {
                        parts = parts[:len(parts)-1]
                }
        }

        return strings.Join(parts, ".")
}

func (e *CompressionExtractor) GetFilenames() ([]string, error) {
        // 检查是否是压缩文件
        magic, err := readFileMagic(e.Filename)
        if err != nil {
                return nil, err
        }

        // 简单的魔数检查
        if !bytes.Contains(magic, []byte("compress")) {
                return nil, fmt.Errorf("doesn't look like a compressed file")
        }

        return []string{e.basename()}, nil
}

func (e *CompressionExtractor) Extract(ignorePasswd bool) error {
        e.IgnorePw = ignorePasswd
        e.ContentType = ONE_ENTRY_KNOWN
        e.ContentName = e.basename()
        e.FileCount = 1
        e.IncludedRoot = "./"

        // 创建临时文件
        outputFile, err := ioutil.TempFile(".", ".unbox-")
        if err != nil {
                return fmt.Errorf("cannot extract here: %v", err)
        }

        e.Target = outputFile.Name()
        outputFile.Close()

        // 执行提取
        if err := e.runPipes(nil); err != nil {
                os.Remove(e.Target)
                return err
        }

        // 检查文件大小
        info, err := os.Stat(e.Target)
        if err != nil {
                os.Remove(e.Target)
                return err
        }

        if info.Size() == 0 {
                os.Remove(e.Target)
                return fmt.Errorf("extracted file is empty")
        }

        return nil
}

// Tar提取器
type TarExtractor struct {
        BaseExtractor
}

func NewTarExtractor(filename, encoding string) (*TarExtractor, error) {
        base, err := NewBaseExtractor(filename, encoding)
        if err != nil {
                return nil, err
        }

        extractor := &TarExtractor{
                BaseExtractor: *base,
        }

        extractor.ExtractPipe = []string{"tar", "-x"}
        extractor.ListPipe = []string{"tar", "-t"}

        return extractor, nil
}

// Zip提取器
type ZipExtractor struct {
        BaseExtractor
}

func NewZipExtractor(filename, encoding string) (*ZipExtractor, error) {
        base, err := NewBaseExtractor(filename, encoding)
        if err != nil {
                return nil, err
        }

        extractor := &ZipExtractor{
                BaseExtractor: *base,
        }

        extractor.ExtractPipe = []string{"unzip", "-q"}
        extractor.ListPipe = []string{"zipinfo", "-1"}

        return extractor, nil
}

func (e *ZipExtractor) isFatalError(status int) bool {
        return status > 1
}

// RAR提取器
type RarExtractor struct {
        BaseExtractor
}

func NewRarExtractor(filename, encoding string) (*RarExtractor, error) {
        base, err := NewBaseExtractor(filename, encoding)
        if err != nil {
                return nil, err
        }

        extractor := &RarExtractor{
                BaseExtractor: *base,
        }

        extractor.ExtractPipe = []string{"unrar", "x"}
        extractor.ListPipe = []string{"unrar", "v"}

        return extractor, nil
}

// 提取器构建器
type ExtractorBuilder struct {
        Filename string
        Options  *Options
}

func NewExtractorBuilder(filename string, options *Options) *ExtractorBuilder {
        return &ExtractorBuilder{
                Filename: filename,
                Options:  options,
        }
}

func (eb *ExtractorBuilder) GetExtractor() (Extractor, error) {
        // 尝试通过多种方法确定文件类型
        extractors := eb.tryByMimetype()
        if len(extractors) > 0 {
                return extractors[0], nil
        }

        extractors = eb.tryByExtension()
        if len(extractors) > 0 {
                return extractors[0], nil
        }

        extractors = eb.tryByMagic()
        if len(extractors) > 0 {
                return extractors[0], nil
        }

        return nil, fmt.Errorf("unknown archive type")
}

func (eb *ExtractorBuilder) tryByMimetype() []Extractor {
        mimetype, encoding := guessMimeType(eb.Filename)

        if mimetype != "" {
                // 根据MIME类型查找提取器
                extractorType, ok := mimetypeMap[mimetype]
                if ok {
                        // 创建提取器
                        switch extractorType {
                        case "tar":
                                if extractor, err := NewTarExtractor(eb.Filename, encoding); err == nil {
                                        return []Extractor{extractor}
                                }
                        case "zip":
                                if extractor, err := NewZipExtractor(eb.Filename, encoding); err == nil {
                                        return []Extractor{extractor}
                                }
                        case "rar":
                                if extractor, err := NewRarExtractor(eb.Filename, encoding); err == nil {
                                        return []Extractor{extractor}
                                }
                        }
                }
        }

        return nil
}

func (eb *ExtractorBuilder) tryByExtension() []Extractor {
        ext := filepath.Ext(filepath.Base(eb.Filename))
        if ext == "" {
                return nil
        }

        ext = strings.TrimPrefix(ext, ".")

        // 根据扩展名查找提取器
        if extractorTypes, ok := extensionMap[ext]; ok {
                for _, et := range extractorTypes {
                        encoding := ""
                        if len(et) > 1 {
                                encoding = et[1]
                        }

                        switch et[0] {
                        case "tar":
                                if extractor, err := NewTarExtractor(eb.Filename, encoding); err == nil {
                                        return []Extractor{extractor}
                                }
                        case "zip":
                                if extractor, err := NewZipExtractor(eb.Filename, encoding); err == nil {
                                        return []Extractor{extractor}
                                }
                        case "rar":
                                if extractor, err := NewRarExtractor(eb.Filename, encoding); err == nil {
                                        return []Extractor{extractor}
                                }
                        }
                }
        }

        return nil
}

func (eb *ExtractorBuilder) tryByMagic() []Extractor {
        magic, err := readFileMagic(eb.Filename)
        if err != nil {
                return nil
        }

        // 根据魔数查找提取器
        for regex, extractorType := range magicMimeMap {
                if regex.Match(magic) {
                        encoding := ""

                        // 检查编码
                        for encRegex, encType := range magicEncodingMap {
                                if encRegex.Match(magic) {
                                        encoding = encType
                                        break
                                }
                        }

                        // 创建提取器
                        switch extractorType {
                        case "tar":
                                if extractor, err := NewTarExtractor(eb.Filename, encoding); err == nil {
                                        return []Extractor{extractor}
                                }
                        case "zip":
                                if extractor, err := NewZipExtractor(eb.Filename, encoding); err == nil {
                                        return []Extractor{extractor}
                                }
                        case "rar":
                                if extractor, err := NewRarExtractor(eb.Filename, encoding); err == nil {
                                        return []Extractor{extractor}
                                }
                        }
                }
        }

        return nil
}

// 处理器接口
type Handler interface {
        Handle() error
}

// 基础处理器
type BaseHandler struct {
        Extractor *BaseExtractor
        Options   *Options
        Target    string
}

func NewBaseHandler(extractor *BaseExtractor, options *Options) *BaseHandler {
        return &BaseHandler{
                Extractor: extractor,
                Options:   options,
        }
}

func (h *BaseHandler) Handle() error {
        // 设置文件权限
        if err := os.Chmod(h.Extractor.Target, 0755); err != nil {
                return err
        }

        // 递归设置目录权限
        err := filepath.Walk(h.Extractor.Target, func(path string, info os.FileInfo, err error) error {
                if err != nil {
                        return err
                }

                if info.IsDir() {
                        if err := os.Chmod(path, 0755); err != nil {
                                return err
                        }
                } else {
                        if err := os.Chmod(path, 0644); err != nil {
                                return err
                        }
                }

                return nil
        })

        if err != nil {
                return err
        }

        return h.organize()
}

func (h *BaseHandler) organize() error {
        return nil
}

func (h *BaseHandler) setTarget(target string, checker FilenameChecker) error {
        checker.OriginalName = target
        checkedTarget, err := checker.Check()
        if err != nil {
                return err
        }

        if checkedTarget != target {
                log.Printf("extracting %s to %s", h.Extractor.Filename, checkedTarget)
        }

        h.Target = checkedTarget
        return nil
}

// 扁平处理器
type FlatHandler struct {
        BaseHandler
}

func NewFlatHandler(extractor *BaseExtractor, options *Options) *FlatHandler {
        return &FlatHandler{
                BaseHandler: *NewBaseHandler(extractor, options),
        }
}

func (h *FlatHandler) organize() error {
        h.Target = "."

        // 递归移动所有文件到当前目录
        err := filepath.Walk(h.Extractor.Target, func(path string, info os.FileInfo, err error) error {
                if err != nil {
                        return err
                }

                if !info.IsDir() {
                        // 获取相对路径
                        relPath, err := filepath.Rel(h.Extractor.Target, path)
                        if err != nil {
                                return err
                        }

                        // 创建目标目录
                        targetDir := filepath.Dir(relPath)
                        if targetDir != "" {
                                if err := os.MkdirAll(targetDir, 0755); err != nil {
                                        return err
                                }
                        }

                        // 移动文件
                        if err := os.Rename(path, relPath); err != nil {
                                return err
                        }
                }

                return nil
        })

        if err != nil {
                return err
        }

        // 删除空目录
        err = filepath.Walk(h.Extractor.Target, func(path string, info os.FileInfo, err error) error {
                if err != nil {
                        return err
                }

                if info.IsDir() {
                        // 检查目录是否为空
                        entries, err := ioutil.ReadDir(path)
                        if err != nil {
                                return err
                        }

                        if len(entries) == 0 {
                                if err := os.Remove(path); err != nil {
                                        return err
                                }
                        }
                }

                return nil
        })

        return err
}

// 覆盖处理器
type OverwriteHandler struct {
        BaseHandler
}

func NewOverwriteHandler(extractor *BaseExtractor, options *Options) *OverwriteHandler {
        return &OverwriteHandler{
                BaseHandler: *NewBaseHandler(extractor, options),
        }
}

func (h *OverwriteHandler) organize() error {
        target := h.Extractor.basename()

        // 如果目标已存在，删除它
        if _, err := os.Stat(target); err == nil {
                if err := os.RemoveAll(target); err != nil {
                        return err
                }
        }

        // 重命名提取目录
        if err := os.Rename(h.Extractor.Target, target); err != nil {
                return err
        }

        h.Target = target
        return nil
}

// 匹配处理器
type MatchHandler struct {
        BaseHandler
}

func NewMatchHandler(extractor *BaseExtractor, options *Options) *MatchHandler {
        return &MatchHandler{
                BaseHandler: *NewBaseHandler(extractor, options),
        }
}

func (h *MatchHandler) organize() error {
        // 获取提取目录中的第一个条目
        entries, err := ioutil.ReadDir(h.Extractor.Target)
        if err != nil {
                return err
        }

        if len(entries) == 0 {
                return errors.New("extracted directory is empty")
        }

        firstEntry := entries[0]
        source := filepath.Join(h.Extractor.Target, firstEntry.Name())

        // 根据条目类型创建检查器
        if firstEntry.IsDir() {
                checker := DirectoryChecker{FilenameChecker{OriginalName: firstEntry.Name()}}
                destination := h.Extractor.basename()

                if err := h.setTarget(destination, checker.FilenameChecker); err != nil {
                        return err
                }

                // 移动目录
                if err := os.Rename(source, h.Target); err != nil {
                        return err
                }

                // 删除空的提取目录
                if err := os.Remove(h.Extractor.Target); err != nil {
                        return err
                }
        } else {
                checker := FilenameChecker{OriginalName: firstEntry.Name()}
                destination := firstEntry.Name()

                if err := h.setTarget(destination, checker); err != nil {
                        return err
                }

                // 移动文件
                if err := os.Rename(h.Extractor.Target, h.Target); err != nil {
                        return err
                }
        }

        h.Extractor.IncludedRoot = "./"
        return nil
}

// 空处理器
type EmptyHandler struct {
        Extractor *BaseExtractor
}

func NewEmptyHandler(extractor *BaseExtractor, options *Options) *EmptyHandler {
        // 删除空的提取目录
        if extractor.Target != "" {
                os.RemoveAll(extractor.Target)
        }

        return &EmptyHandler{
                Extractor: extractor,
        }
}

func (h *EmptyHandler) Handle() error {
        return nil
}

// 炸弹处理器
type BombHandler struct {
        BaseHandler
}

func NewBombHandler(extractor *BaseExtractor, options *Options) *BombHandler {
        return &BombHandler{
                BaseHandler: *NewBaseHandler(extractor, options),
        }
}

func (h *BombHandler) organize() error {
        basename := h.Extractor.basename()
        checker := DirectoryChecker{FilenameChecker{OriginalName: basename}}

        if err := h.setTarget(basename, checker.FilenameChecker); err != nil {
                return err
        }

        // 重命名提取目录
        if err := os.Rename(h.Extractor.Target, h.Target); err != nil {
                return err
        }

        return nil
}

// 提取动作
type ExtractionAction struct {
        Options         *Options
        Filenames       []string
        CurrentFilename string
        CurrentHandler  Handler
        Successes       []string
        Failures        []string
}

func NewExtractionAction(options *Options, filenames []string) *ExtractionAction {
        return &ExtractionAction{
                Options:   options,
                Filenames: filenames,
        }
}

func (a *ExtractionAction) Run() int {
        for _, filename := range a.Filenames {
                if err := a.processFile(filename); err != nil {
                        log.Printf("Error processing %s: %v", filename, err)
                        a.Failures = append(a.Failures, filename)
                } else {
                        a.Successes = append(a.Successes, filename)
                }
        }

        if len(a.Failures) > 0 {
                return 1
        }

        return 0
}

func (a *ExtractionAction) processFile(filename string) error {
        a.CurrentFilename = filename

        // 构建提取器
        builder := NewExtractorBuilder(filename, a.Options)
        extractor, err := builder.GetExtractor()
        if err != nil {
                return err
        }

        // 提取文件
        if err := extractor.Extract(a.Options.Batch); err != nil {
                return err
        }

        // 获取处理器
        handler, err := a.getHandler(extractor.GetBase())
        if err != nil {
                return err
        }

        a.CurrentHandler = handler

        // 处理提取结果
        if err := handler.Handle(); err != nil {
                return err
        }

        // 显示提取结果
        if err := a.showExtraction(extractor.GetBase()); err != nil {
                return err
        }

        // 递归处理包含的归档文件
        baseExtractor := extractor.GetBase()
        if len(baseExtractor.IncludedArchives) > 0 {
                a.Options.RecursionPolicy.CurrentPolicy = RECURSE_NOT_NOW

                // 检查是否应该递归
                if a.Options.Recursive || a.askRecurse(baseExtractor) {
                        // 递归处理
                        for _, includedFile := range baseExtractor.IncludedArchives {
                                includedPath := filepath.Join(baseExtractor.Target, includedFile)
                                if err := a.processFile(includedPath); err != nil {
                                        log.Printf("Error processing included archive %s: %v", includedFile, err)
                                }
                        }
                }
        }

        return nil
}

func (a *ExtractionAction) getHandler(extractor *BaseExtractor) (Handler, error) {
        if extractor.ContentType == EMPTY {
                return NewEmptyHandler(extractor, a.Options), nil
        }

        if a.Options.Flat && extractor.ContentType != ONE_ENTRY_KNOWN {
                return NewFlatHandler(extractor, a.Options), nil
        }

        if a.Options.Overwrite && extractor.ContentType == MATCHING_DIRECTORY {
                return NewOverwriteHandler(extractor, a.Options), nil
        }

        if extractor.ContentType == MATCHING_DIRECTORY ||
                (extractor.ContentType == ONE_ENTRY_KNOWN && a.Options.OneEntryPolicy.CurrentPolicy == EXTRACT_HERE) {
                return NewMatchHandler(extractor, a.Options), nil
        }

        return NewBombHandler(extractor, a.Options), nil
}

func (a *ExtractionAction) showExtraction(extractor *BaseExtractor) error {
        if a.Options.LogLevel > 0 {
                return nil
        }

        if len(a.Filenames) < 2 {
                return nil
        }

        fmt.Printf("\n%s:\n", a.CurrentFilename)

        if extractor.Contents == nil {
                fmt.Println(a.CurrentHandler.(*BaseHandler).Target)
                return nil
        }

        var filenames []string
        if a.CurrentHandler.(*BaseHandler).Target == "." {
                filenames = extractor.Contents
        } else {
                filenames = []string{a.CurrentHandler.(*BaseHandler).Target}
        }

        // 递归显示文件列表
        for _, filename := range filenames {
                err := filepath.Walk(filename, func(path string, info os.FileInfo, err error) error {
                        if err != nil {
                                return err
                        }

                        if info.IsDir() {
                                fmt.Printf("%s/\n", path)
                        } else {
                                fmt.Println(path)
                        }

                        return nil
                })
                if err != nil {
                        return err
                }
        }

        return nil
}

func (a *ExtractionAction) askRecurse(extractor *BaseExtractor) bool {
        if a.Options.Batch {
                return a.Options.Recursive
        }

        archiveCount := len(extractor.IncludedArchives)
        fileCount := extractor.FileCount

        fmt.Printf("\n%s contains %d other archive file(s), out of %d file(s) total.\n",
                a.CurrentFilename, archiveCount, fileCount)

        fmt.Println("\nYou can:")
        fmt.Println("  A: Always extract included archives during this session")
        fmt.Println("  O: Extract included archives this once")
        fmt.Println("  N: Choose not to extract included archives this once")
        fmt.Println("  V: Never extract included archives during this session")
        fmt.Println("  L: List included archives")

        for {
                fmt.Print("\nWhat do you want to do? (a/o/N/v/l) ")
                var choice string
                _, err := fmt.Scan(&choice)
                if err != nil {
                        return false
                }
                choice = strings.ToLower(choice)

                switch choice {
                case "a":
                        a.Options.RecursionPolicy.CurrentPolicy = RECURSE_ALWAYS
                        return true
                case "o":
                        a.Options.RecursionPolicy.CurrentPolicy = RECURSE_ONCE
                        return true
                case "n":
                        a.Options.RecursionPolicy.CurrentPolicy = RECURSE_NOT_NOW
                        return false
                case "v":
                        a.Options.RecursionPolicy.CurrentPolicy = RECURSE_NEVER
                        return false
                case "l":
                        fmt.Println("\nIncluded archives:")
                        for _, file := range extractor.IncludedArchives {
                                fmt.Println("  " + file)
                        }
                        fmt.Println()
                default:
                        fmt.Println("Invalid choice, please try again.")
                }
        }
}

// 列表动作
type ListAction struct {
        Options         *Options
        Filenames       []string
        CurrentFilename string
        Successes       []string
        Failures        []string
}

func NewListAction(options *Options, filenames []string) *ListAction {
        return &ListAction{
                Options:   options,
                Filenames: filenames,
        }
}

func (a *ListAction) Run() int {
        for _, filename := range a.Filenames {
                if err := a.processFile(filename); err != nil {
                        log.Printf("Error listing %s: %v", filename, err)
                        a.Failures = append(a.Failures, filename)
                } else {
                        a.Successes = append(a.Successes, filename)
                }
        }

        if len(a.Failures) > 0 {
                return 1
        }

        return 0
}

func (a *ListAction) processFile(filename string) error {
        a.CurrentFilename = filename

        // 构建提取器
        builder := NewExtractorBuilder(filename, a.Options)
        extractor, err := builder.GetExtractor()
        if err != nil {
                return err
        }

        // 列出文件内容
        filenames, err := extractor.GetFilenames()
        if err != nil {
                return err
        }

        // 显示文件列表
        a.showFilenames(filenames)

        return nil
}

func (a *ListAction) showFilenames(filenames []string) {
        if len(a.Filenames) > 1 {
                fmt.Printf("\n%s:\n", a.CurrentFilename)
        }

        for _, filename := range filenames {
                fmt.Println(filename)
        }
}

// 应用程序
type ExtractorApplication struct {
        Options   *Options
        Archives  map[string][]string
        Successes []string
        Failures  []string
}

func NewExtractorApplication() *ExtractorApplication {
        return &ExtractorApplication{
                Archives: make(map[string][]string),
        }
}

func (app *ExtractorApplication) Run(arguments []string) int {
        app.parseOptions(arguments)
        app.setupLogger()

        // 初始化档案列表
        workingDir, err := os.Getwd()
        if err != nil {
                log.Fatalf("Failed to get working directory: %v", err)
        }

        app.Archives[workingDir] = app.Options.Filenames

        // 处理所有档案
        for len(app.Archives) > 0 {
                // 获取下一个目录和档案列表
                directory, filenames := app.popArchive()

                // 切换到该目录
                oldDir, err := os.Getwd()
                if err != nil {
                        log.Fatalf("Failed to get working directory: %v", err)
                }

                if err := os.Chdir(directory); err != nil {
                        log.Printf("Failed to change to directory %s: %v", directory, err)
                        app.Failures = append(app.Failures, filenames...)
                        continue
                }

                // 处理该目录下的所有档案
                for _, filename := range filenames {
                        // 下载远程档案
                        downloaded, err := app.downloadFile(filename)
                        if err != nil {
                                log.Printf("Failed to download %s: %v", filename, err)
                                app.Failures = append(app.Failures, filename)
                                continue
                        }

                        // 检查文件
                        if err := app.checkFile(downloaded); err != nil {
                                log.Printf("%s: %v", downloaded, err)
                                app.Failures = append(app.Failures, downloaded)
                                continue
                        }

                        // 执行提取或列表操作
                        if app.Options.ShowList {
                                action := NewListAction(app.Options, []string{downloaded})
                                if action.Run() != 0 {
                                        app.Failures = append(app.Failures, downloaded)
                                } else {
                                        app.Successes = append(app.Successes, downloaded)
                                }
                        } else {
                                action := NewExtractionAction(app.Options, []string{downloaded})
                                if action.Run() != 0 {
                                        app.Failures = append(app.Failures, downloaded)
                                } else {
                                        app.Successes = append(app.Successes, downloaded)
                                }
                        }
                }

                // 恢复工作目录
                if err := os.Chdir(oldDir); err != nil {
                        log.Printf("Failed to change back to working directory: %v", err)
                }
        }

        // 输出结果
        if len(app.Failures) > 0 {
                log.Printf("Failed to process %d archive(s)", len(app.Failures))
                return 1
        }

        log.Printf("Successfully processed all archives")
        return 0
}

func (app *ExtractorApplication) parseOptions(arguments []string) {
        // 解析命令行参数
        showList := flag.Bool("l", false, "list contents of archives")
        showList2 := flag.Bool("t", false, "list contents of archives")
        metadata := flag.Bool("m", false, "extract metadata from a .deb/.gem")
        recursive := flag.Bool("r", false, "extract archives contained in the ones listed")
        oneEntryDefault := flag.String("one", "", "specify extraction policy for one-entry archives: inside/rename/here")
        batch := flag.Bool("n", false, "noninteractive mode")
        overwrite := flag.Bool("o", false, "overwrite existing targets")
        flat := flag.Bool("f", false, "extract everything to current directory")
        verbose := flag.Int("v", 0, "verbose mode")
        quiet := flag.Int("q", 3, "quiet mode")

        flag.Usage = func() {
                fmt.Fprintf(os.Stderr, "Usage: %s [options] archive [archive2 ...]\n", os.Args[0])
                fmt.Fprintf(os.Stderr, "Unbox (Intelligent archive extractor)\n\n")
                fmt.Fprintf(os.Stderr, "Options:\n")
                flag.PrintDefaults()
        }

        flag.Parse()

        filenames := flag.Args()
        if len(filenames) == 0 {
                flag.Usage()
                os.Exit(1)
        }

        // 设置选项
        app.Options = &Options{
                ShowList:        *showList || *showList2,
                Metadata:        *metadata,
                Recursive:       *recursive,
                OneEntryDefault: *oneEntryDefault,
                Batch:           *batch,
                Overwrite:       *overwrite,
                Flat:            *flat,
                LogLevel:        10 * (*quiet - *verbose),
        }

        // 初始化策略
        app.Options.OneEntryPolicy = OneEntryPolicy{
                CurrentPolicy: EXTRACT_WRAP,
        }

        app.Options.RecursionPolicy = RecursionPolicy{
                CurrentPolicy: RECURSE_NOT_NOW,
        }

        app.Options.Filenames = filenames
}

func (app *ExtractorApplication) setupLogger() {
        log.SetFlags(0)
        log.SetOutput(os.Stderr)
        log.SetPrefix("unbox: ")
}

func (app *ExtractorApplication) popArchive() (string, []string) {
        for dir, files := range app.Archives {
                delete(app.Archives, dir)
                return dir, files
        }
        return "", nil
}

func (app *ExtractorApplication) downloadFile(filename string) (string, error) {
        // 检查是否是URL
        parsed, err := url.Parse(filename)
        if err != nil {
                return filename, nil
        }

        if parsed.Scheme != "" && (parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "ftp") {
                // 下载文件
                baseName := filepath.Base(parsed.Path)
                cmd := exec.Command("wget", "-c", filename)
                if err := cmd.Run(); err != nil {
                        return "", fmt.Errorf("wget failed: %v", err)
                }
                return baseName, nil
        }

        return filename, nil
}

func (app *ExtractorApplication) checkFile(filename string) error {
        info, err := os.Stat(filename)
        if err != nil {
                return err
        }

        if info.IsDir() {
                return errors.New("cannot work with a directory")
        }

        return nil
}

// 全局映射
var (
        mimetypesEncodingsMap = map[string]string{
                ".bz2":  "bzip2",
                ".lzma": "lzma",
                ".xz":   "xz",
                ".lz":   "lzip",
                ".lrz":  "lrzip",
                ".zst":  "zstd",
                ".zstd": "zstd",
        }

        mimetypesTypesMap = map[string]string{
                ".gem": "application/x-ruby-gem",
        }

        mimetypesCommonTypes = map[string]string{
                ".tar": "application/x-tar",
                ".zip": "application/zip",
        }

        mimetypeMap = map[string]string{
                "application/x-tar":             "tar",
                "application/zip":               "zip",
                "application/x-rpm":             "rpm",
                "application/x-debian-package":  "deb",
                "application/x-cpio":            "cpio",
                "application/x-ruby-gem":        "gem",
                "application/x-7z-compressed":   "7z",
                "application/x-cab":             "cab",
                "application/rar":               "rar",
        }

        extensionMap = map[string][][]string{
                "tar":       {{"tar", ""}},
                "tar.bz2":   {{"tar", "bzip2"}},
                "tar.gz":    {{"tar", "gzip"}},
                "tar.lzma":  {{"tar", "lzma"}},
                "tar.xz":    {{"tar", "xz"}},
                "tar.lz":    {{"tar", "lzip"}},
                "tar.Z":     {{"tar", "compress"}},
                "tar.lrz":   {{"tar", "lrzip"}},
                "tar.zst":   {{"tar", "zstd"}},
                "zip":       {{"zip", ""}},
                "jar":       {{"zip", ""}},
                "epub":      {{"zip", ""}},
                "xpi":       {{"zip", ""}},
                "crx":       {{"zip", ""}},
                "rpm":       {{"rpm", ""}},
                "deb":       {{"deb", ""}},
                "cpio":      {{"cpio", ""}},
                "gem":       {{"gem", ""}},
                "7z":        {{"7z", ""}},
                "cab":       {{"cab", ""}},
                "rar":       {{"rar", ""}},
                "arj":       {{"arj", ""}},
                "msi":       {{"msi", ""}},
                "dmg":       {{"dmg", ""}},
                "zst":       {{"zstd", ""}},
                "zstd":      {{"zstd", ""}},
                "br":        {{"brotli", ""}},
        }

        magicMimeMap = map[*regexp.Regexp]string{
                regexp.MustCompile("POSIX tar archive"):       "tar",
                regexp.MustCompile("Zip archive"):             "zip",
                regexp.MustCompile("RPM"):                    "rpm",
                regexp.MustCompile("Debian binary package"):  "deb",
                regexp.MustCompile("cpio archive"):            "cpio",
                regexp.MustCompile("7-zip archive"):          "7z",
                regexp.MustCompile("Microsoft Cabinet Archive"): "cab",
                regexp.MustCompile("RAR archive"):            "rar",
                regexp.MustCompile("InstallShield CAB"):      "shield",
                regexp.MustCompile("Windows Installer"):      "msi",
                regexp.MustCompile("ISO 9660 CD-ROM"):        "dmg",
                regexp.MustCompile("Zstandard compressed"):   "zstd",
        }

        magicEncodingMap = map[*regexp.Regexp]string{
                regexp.MustCompile("bzip2 compressed"):      "bzip2",
                regexp.MustCompile("gzip compressed"):       "gzip",
                regexp.MustCompile("LZMA compressed"):       "lzma",
                regexp.MustCompile("lzip compressed"):       "lzip",
                regexp.MustCompile("LRZIP compressed"):      "lrzip",
                regexp.MustCompile("Zstandard compressed"):  "zstd",
                regexp.MustCompile("xz compressed"):         "xz",
        }
)

func guessMimeType(filename string) (string, string) {
        ext := filepath.Ext(filename)
        mimeType := mimetypesCommonTypes[ext]
        encoding := mimetypesEncodingsMap[ext]
        return mimeType, encoding
}

func readFileMagic(filename string) ([]byte, error) {
        // 读取文件前512字节作为魔数
        file, err := os.Open(filename)
        if err != nil {
                return nil, err
        }
        defer file.Close()

        // 限制读取大小为512字节
        buffer := make([]byte, 512)
        n, err := file.Read(buffer)
        if err != nil && err != io.EOF {
                return nil, err
        }

        return buffer[:n], nil
}

func isArchiveFile(filename string) bool {
        ext := filepath.Ext(filename)
        _, isEncoding := mimetypesEncodingsMap[ext]
        _, isType := mimetypesTypesMap[ext]
        _, isExt := extensionMap[strings.TrimPrefix(ext, ".")]

        return isEncoding || isType || isExt
}

func main() {
        app := NewExtractorApplication()
        os.Exit(app.Run(os.Args[1:]))
}
