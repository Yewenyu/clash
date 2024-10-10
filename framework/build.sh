# 开启 Go modules 支持
export GO111MODULE=on
# 设置 Go module 的代理
export GOPROXY=https://goproxy.cn

# 清理并下载缺失的模块，移除不用的模块
go mod tidy

# 安装 gomobile 和 gobind
go install golang.org/x/mobile/cmd/gomobile@latest
go get golang.org/x/mobile/cmd/gobind
# go install -v github.com/sagernet/gomobile/cmd/gomobile@v0.1.3
# go install -v github.com/sagernet/gomobile/cmd/gobind@v0.1.3
# go get github.com/sagernet/gomobile


# 设置 PATH
export PATH=$PATH:$(go env GOPATH)/bin

# 初始化 gomobile
gomobile init

# 检查是否为调试模式
if [ "$1" == "debug" ]; then
    echo "Building in debug mode..."
    export GOGCFLAGS="-N -l"
else
    unset GOGCFLAGS
    echo "Building in release mode..."
fi

# 绑定 iOS, macOS, iOS 模拟器
gomobile bind -target=ios,macos,iossimulator -o=framework/Clash.xcframework -bootclasspath=.. -iosversion=11.0 -v
# gomobile bind  -target=ios,macos,iossimulator,tvos,tvossimulator -o=framework/build/Clash.xcframework -bootclasspath=.. -iosversion=11.0 -v

# 打开 framework 文件夹
open framework/