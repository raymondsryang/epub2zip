#!/bin/bash

# 项目名
APP_NAME="Epub2Zip"

# 检查 Go 是否安装
if ! command -v go &> /dev/null; then
    echo "Go 未安装，请先安装"
    exit 1
fi

# 检查 Fyne CLI (新路径) 是否安装
if ! command -v fyne &> /dev/null; then
    echo "Fyne CLI 未安装，请使用 'go install fyne.io/tools/cmd/fyne@latest' 安装"
    exit 1
fi

# 清理之前的构建
echo "清理之前的构建..."
rm -rf ${APP_NAME}.app

# 使用 fyne 命令构建 macOS 应用
echo "开始构建 macOS 应用..."
fyne package -os darwin -icon "icon.png" -name "${APP_NAME}"

if [ $? -eq 0 ]; then
    echo "构建成功！应用程序是: ${APP_NAME}.app"
else
    echo "构建失败！"
fi