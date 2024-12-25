package main

import "os"

func main() {

	// 打开或创建文件
	file, err := os.OpenFile("text.json", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return
	}
	defer file.Close()

	text := "sdss"
	// 写入数据到文件
	if _, err := file.Write([]byte(text)); err != nil {
		return
	}

}
