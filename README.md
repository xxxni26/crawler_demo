简介:
一个用 Go + Colly 编写的爬虫工具，用于从 第一范文网 搜索并采集文章。支持关键词搜索、分页、文章去重、编码转换，并将结果保存为 CSV 文件。

功能:
输入关键词和目标文章数，自动抓取对应内容
提取标题、时间、正文、链接
支持分页抓取和简单去重
自动处理 GBK → UTF-8 编码
输出 CSV 文件（带 BOM，避免中文乱码）

使用方法:

1.环境准备
go mod init xxx

go mod tidy

2.运行
go run main.go

输入示例：

请输入搜索关键词: 简历范文

请输入需要爬取的文章数量: 10


结果会保存到：
简历范文.csv

输出说明:

CSV 字段
标题
时间
内容
链接

注意事项:

本工具仅供学习使用，请遵守目标站点 Robots 协议
抓取速度可在代码中调整：

c.Limit(&colly.LimitRule{
    Parallelism: 2,
    Delay: 2 * time.Second,
})
