# Unbox - 智能解压工具

`Unbox` 是一个命令行归档文件解压工具，支持多种压缩格式并能处理嵌套归档

## 功能特性

- **多格式支持**：自动检测并解压 `tar`, `zip`, `rar`, `gzip`, `bzip2`, `xz`, `zstd`, `7z` 等格式
- **递归解压**：自动检测并解压嵌套的归档文件 (`-r` 选项)
- **安全操作**：
  - 防止路径遍历攻击
- **批量处理**：支持同时解压多个文件

## 安装

### 源码安装 (需Go 1.20+)
```bash
go install github.com/Geekstrange/Unbox@latest
```

### 依赖说明

部分格式需要系统安装：

```bash
# Debian/Ubuntu
sudo apt install unrar p7zip-full zstd

# RHEL/CentOS
sudo yum install unrar p7zip zstd
```

## 使用指南

### 基本用法

```bash
Unbox archive.zip        # 创建archive目录并解压
Unbox file1.tar file2.zip # 批量解压
```

### 常用选项

|    选项     |       描述       |         示例          |
| :---------: | :--------------: | :-------------------: |
|    `-r`     | 递归解压嵌套归档 | `Unbox -r bundle.zip` |
|    `-l`     |   列出归档内容   | `Unbox -l files.rar`  |
|    `-o`     | 解压后删除源文件 | `Unbox -o update.zip` |
|    `-s`     | 显示支持格式列表 |      `Unbox -s`       |
|    `-h`     |   显示帮助信息   |      `Unbox -h`       |
| `--version` |   显示版本信息   |   `Unbox --version`   |

### 递归解压示例

```bash
Unbox -r software_bundle.zip
# 输出:
# 正在解压 software_bundle.zip 到 software_bundle...
# 开始递归解压嵌套文件: software_bundle
```

## 解压行为说明

1. 默认创建与归档同名的目录（不含扩展名）
2. 所有内容解压到该目录中
3. 目录权限设为755，文件权限设为644
4. 递归解压时会删除已解压的嵌套归档

## 常见问题

**Q: 解压后文件权限不正确？**
A: 工具会自动设置目录为755，文件为644权限

**Q: 为什么解压后多了一层目录？**
A: 这是默认行为，避免污染当前目录

**Q: 如何批量解压当前目录所有zip文件？**

```bash
Unbox *.zip
```

**Q: 支持哪些格式？**
A: 运行 `Unbox -s` 查看完整列表：

```bash
7z
Z
arj
br
bz2
...（完整列表见实际输出）
```

**Q: rar/7z解压报错？**
A: 确保系统已安装 `unrar` 和 `7z` 命令

## 技术说明

```mermaid
graph TD
    A[命令行输入] --> B[解析参数]
    B --> C[识别文件类型]
    C --> D[创建解压目录]
    D --> E[调用对应解压器]
    E --> F[递归检测嵌套归档]
    F --> G[递归解压]
    G --> H[完成]
```

## 版本选择指南

根据您的操作系统和架构，请选择对应的安装文件以下是不同平台的版本对应关系：

| **操作系统** | **架构**              | **文件名**                      |
| :----------- | :-------------------- | :------------------------------ |
| **Windows**  | x86_64/AMD64          | `unbox-vx.x.x-windows_amd64.exe` |
|              | ARM64/AArch64         | `unbox-vx.x.x-windows_arm64.exe` |
| **Linux**    | x86_64/AMD64          | `unbox-vx.x.x-linux_amd64`       |
|              | ARM64/AArch64         | `unbox-vx.x.x-linux_arm64`       |
|              | LoongArch         | `unbox-vx.x.x-linux_loong64`       |
| **macOS**    | Intel (x86_64/AMD64)        | `unbox-vx.x.x-darwin_amd64`      |
|              | Apple Silicon (ARM64/AArch64) | `unbox-vx.x.x-darwin_arm64`      |

## 如何确定我的系统架构

### Windows 系统

1. **打开命令提示符**：可以通过在开始菜单中搜索“cmd”或“命令提示符”来打开

2. **输入命令**：在命令提示符中输入以下命令并按回车键：

   ```cmd
   wmic os get osarchitecture
   ```

3. **查看输出结果**：

   - 如果显示“64-bit”，则您的系统是 **64位 (x86_64)**
   - 如果显示“ARM64”，则您的系统是 **ARM64**

### Linux 系统

1. **打开终端**：可以通过在应用程序菜单中搜索“终端”或使用快捷键（通常是`Ctrl+Alt+T`）来打开

2. **输入命令**：在终端中输入以下命令并按回车键：

   ```bash
   uname -m
   ```

3. **查看输出结果**：

   - 如果显示`x86_64`，则您的系统是 **64位 (x86_64)**
   - 如果显示`aarch64`，则您的系统是 **ARM64**

### macOS 系统

1. **打开终端**：可以通过在应用程序菜单中搜索“终端”来打开

2. **输入命令**：在终端中输入以下命令并按回车键：

   ```zsh
   uname -m
   ```

3. **查看输出结果**：

   - 如果显示`x86_64`，则您的系统是 **Intel (x86_64)**
   - 如果显示`arm64`，则您的系统是 **Apple Silicon (ARM64)**

## 许可证

<a href="https://www.apache.org/licenses/LICENSE-2.0.html" target="_blank">
  <img
    src="https://github.com/Geekstrange/unbox/blob/main/img/Apache-License.svg"
    alt="Apache License 2.0"
    width="180"
    height="auto">
</a>

Apache License 2.0 - 详情见项目 LICENSE 文件
