package main

import (
	"encoding/csv"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/gocolly/colly/v2/extensions"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// Article 表示一篇文章的基本信息
type Article struct {
	Title       string
	PublishTime string
	Content     string
	URL         string
}

func main() {
	var keyword string
	var numArticles int

	// 获取用户输入
	fmt.Print("请输入搜索关键词: ")
	fmt.Scanln(&keyword)
	fmt.Print("请输入需要爬取的文章数: ")
	fmt.Scanln(&numArticles)

	fmt.Printf("搜索关键词: %s\n需要爬取的文章数量: %d\n\n", keyword, numArticles)

	// 存储爬取的文章和用于去重的URL集合
	var articles []Article
	var mutex sync.Mutex
	var wg sync.WaitGroup
	urlSet := make(map[string]bool)

	// 创建爬虫实例
	c := colly.NewCollector(
		colly.AllowedDomains("www.diyifanwen.com"),
		colly.MaxDepth(3),
		colly.Async(true),
		colly.IgnoreRobotsTxt(),
	)

	// 设置请求限制
	c.Limit(&colly.LimitRule{
		DomainGlob:  "*",
		Parallelism: 3,
		Delay:       2 * time.Second,
	})

	// 添加随机User-Agent和请求头
	extensions.RandomUserAgent(c)
	c.OnRequest(func(r *colly.Request) {
		r.Headers.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
		r.Headers.Set("Accept-Language", "zh-CN,zh;q=0.9")
		r.Headers.Set("Connection", "keep-alive")
		r.Headers.Set("Upgrade-Insecure-Requests", "1")
	})

	// 处理HTTP错误
	c.OnError(func(r *colly.Response, err error) {
		fmt.Printf("请求错误: %s, 状态码: %d\n", r.Request.URL, r.StatusCode)
	})

	// 处理页面响应，转换编码
	c.OnResponse(func(r *colly.Response) {
		// 尝试转换编码
		utf8Body, err := convertToUTF8(r.Body)
		if err == nil {
			r.Body = utf8Body
		}

		// 保存搜索结果页面用于调试
		if strings.Contains(r.Request.URL.String(), "search") {
			ioutil.WriteFile("search_result.html", r.Body, 0644)
		}
	})

	// 查找文章列表 - 处理demo_box区域
	c.OnHTML("div.demo_box", func(e *colly.HTMLElement) {
		extractArticleLinks(e, &articles, &mutex, &wg, urlSet, numArticles, "")
	})

	// 处理articlelist区域
	c.OnHTML("div.articlelist", func(e *colly.HTMLElement) {
		e.ForEach("a", func(i int, el *colly.HTMLElement) {
			extractArticleLinks(el, &articles, &mutex, &wg, urlSet, numArticles, "articlelist")
		})
	})

	// 处理通用链接
	c.OnHTML("a", func(e *colly.HTMLElement) {
		url := e.Request.URL.String()
		if (strings.Contains(url, "search") || strings.Contains(url, keyword)) {
			extractArticleLinks(e, &articles, &mutex, &wg, urlSet, numArticles, "通用")
		}
	})

	// 处理分页
	c.OnHTML("div.page", func(e *colly.HTMLElement) {
		handlePagination(e, c, &mutex, urlSet, len(articles), numArticles)
	})

	// 使用百度站内搜索
	encodedKeyword := url.QueryEscape(keyword)
	searchURL := fmt.Sprintf("https://zhannei.baidu.com/cse/search?s=15991277701392786341&entry=1&q=%s", encodedKeyword)
	c.AllowedDomains = append(c.AllowedDomains, "zhannei.baidu.com", "www.baidu.com")

	if err := c.Visit(searchURL); err != nil {
		fmt.Printf("爬取失败: %v\n", err)
		return
	}

	// 等待所有爬取任务完成
	c.Wait()
	wg.Wait()

	// 保存到CSV文件
	saveToCSV(articles, keyword+".csv")

	fmt.Printf("\n数据已成功保存到 %s 文件\n共成功爬取 %d 篇文章\n", keyword+".csv", len(articles))
}

// extractArticleLinks 从HTML元素中提取文章链接并处理
func extractArticleLinks(e *colly.HTMLElement, articles *[]Article, mutex *sync.Mutex, wg *sync.WaitGroup, urlSet map[string]bool, maxCount int, source string) {
	// 检查是否已达到目标数量
	mutex.Lock()
	if len(*articles) >= maxCount {
		mutex.Unlock()
		return
	}
	mutex.Unlock()

	href := e.Attr("href")
	text := strings.TrimSpace(e.Text)

	// 检查是否是有效的文章链接
	if (strings.HasSuffix(href, ".htm") || strings.HasSuffix(href, ".html")) && text != "" && len(text) > 5 {
		fullURL := e.Request.AbsoluteURL(href)

		mutex.Lock()
		if !urlSet[fullURL] && len(*articles) < maxCount {
			urlSet[fullURL] = true
			if source != "" {
				fmt.Printf("找到文章链接(%s): %s, 标题: %s\n", source, fullURL, text)
			} else {
				fmt.Printf("找到文章链接: %s, 标题: %s\n", fullURL, text)
			}

			wg.Add(1)
			go func(url string) {
				defer wg.Done()
				scrapeArticle(url, articles, mutex, maxCount)
			}(fullURL)
		}
		mutex.Unlock()
	}
}

// handlePagination 处理分页逻辑
func handlePagination(e *colly.HTMLElement, c *colly.Collector, mutex *sync.Mutex, urlSet map[string]bool, currentCount, maxCount int) {
	// 检查是否已达到目标数量
	mutex.Lock()
	if currentCount >= maxCount {
		mutex.Unlock()
		return
	}
	mutex.Unlock()

	e.ForEach("a", func(_ int, el *colly.HTMLElement) {
		pageText := strings.TrimSpace(el.Text)
		pageHref := el.Attr("href")

		// 查找下一页按钮或页码链接
		if (strings.Contains(pageText, "下一页") || strings.Contains(pageHref, "page=") || strings.Contains(pageHref, "list_")) && !strings.Contains(pageHref, "#") {
			fullPageURL := el.Request.AbsoluteURL(pageHref)

			mutex.Lock()
			if !urlSet[fullPageURL] && len(urlSet) < maxCount*2 { // 限制分页数量
				urlSet[fullPageURL] = true
				fmt.Printf("正在访问分页: %s\n", fullPageURL)
				c.Visit(fullPageURL)
			}
			mutex.Unlock()
		}
	})
}

// scrapeArticle 爬取单篇文章内容
func scrapeArticle(url string, articles *[]Article, mutex *sync.Mutex, maxCount int) {
	// 创建一个新的Colly实例来爬取文章内容
	collector := colly.NewCollector(colly.AllowedDomains("www.diyifanwen.com"))
	extensions.RandomUserAgent(collector)

	var article Article
	article.URL = url

	// 提取标题和内容
	collector.OnHTML("html", func(detailE *colly.HTMLElement) {
		// 提取标题
		title := extractTitle(detailE)
		article.Title = strings.TrimSpace(title)

		// 提取发布时间
		timeStr := extractPublishTime(detailE)
		article.PublishTime = timeStr

		// 提取正文内容
		content := extractContent(detailE)
		article.Content = content

		// 显示文章预览
		if len(article.Content) > 100 {
			fmt.Printf("已爬取: %s... (内容长度: %d)\n", article.Title, len(article.Content))
		} else {
			fmt.Printf("已爬取: %s (内容长度: %d)\n", article.Title, len(article.Content))
		}
	})

	// 处理编码
	collector.OnResponse(func(r *colly.Response) {
		if utf8Body, err := convertToUTF8(r.Body); err == nil {
			r.Body = utf8Body
		}
	})

	// 发送请求并等待完成
	collector.Visit(url)
	collector.Wait()

	// 如果获取到了有效内容，添加到结果中
	if article.Title != "" && len(article.Content) > 20 {
		mutex.Lock()
		if len(*articles) < maxCount {
			*articles = append(*articles, article)
		}
		mutex.Unlock()
	}
}

// extractTitle 从HTML元素中提取文章标题
func extractTitle(e *colly.HTMLElement) string {
	// 尝试多种选择器提取标题
	titleSelectors := []string{"h1", "h2", ".title", ".article-title"}
	for _, selector := range titleSelectors {
		if title := e.ChildText(selector); title != "" {
			return title
		}
	}

	// 使用页面标题作为兜底
	pageTitle := e.ChildText("title")
	for _, suffix := range []string{" - 第一范文网", "_第一范文网", "_第一范文", " - 第一范文"} {
		pageTitle = strings.Replace(pageTitle, suffix, "", 1)
	}
	return pageTitle
}

// extractPublishTime 从HTML元素中提取发布时间
func extractPublishTime(e *colly.HTMLElement) string {
	timeSelectors := []string{
		".info time", ".info .time", ".info span", ".article-meta time",
		".pubtime", ".publish-time", ".time", ".date",
	}

	for _, selector := range timeSelectors {
		if timeText := e.ChildText(selector); timeText != "" {
			timeText = strings.ReplaceAll(timeText, "发布时间：", "")
			timeText = strings.ReplaceAll(timeText, "发表时间：", "")
			timeText = strings.TrimSpace(timeText)

			// 使用正则表达式提取日期格式
			dateRegex := regexp.MustCompile(`\d{4}[-/]\d{1,2}[-/]\d{1,2}`)
			if matches := dateRegex.FindStringSubmatch(timeText); len(matches) > 0 {
				return matches[0]
			}
			return timeText
		}
	}
	return ""
}

// extractContent 从HTML元素中提取文章内容
func extractContent(e *colly.HTMLElement) string {
	contentSelectors := []string{
		".content", ".article-content", ".content-box", ".article-body", ".text",
		"#content", "#article-content", "#article_body", ".neirong", ".articleText",
		".main-text", ".article-main",
	}

	// 尝试标准选择器
	for _, selector := range contentSelectors {
		if content := e.ChildText(selector); len(content) > 20 {
			return cleanContent(content)
		}
	}

	// 尝试article标签
	if content := e.ChildText("article"); len(content) > 20 {
		return cleanContent(content)
	}

	// 尝试合并所有p标签
	var pTexts []string
	e.ForEach("p", func(i int, pEl *colly.HTMLElement) {
		pText := strings.TrimSpace(pEl.Text)
		if len(pText) > 5 {
			pTexts = append(pTexts, pText)
		}
	})

	return cleanContent(strings.Join(pTexts, "\n\n"))
}

// saveToCSV 将爬取的文章保存为CSV文件
func saveToCSV(articles []Article, filename string) {
	file, err := os.Create(filename)
	if err != nil {
		fmt.Printf("创建CSV文件失败: %v\n", err)
		return
	}
	defer file.Close()

	// 写入UTF-8 BOM标记，解决Windows下中文乱码问题
	file.Write([]byte{0xEF, 0xBB, 0xBF})

	writer := csv.NewWriter(file)
	writer.Comma = ','
	defer writer.Flush()

	// 写入表头
	headers := []string{"标题", "发布时间", "内容", "URL"}
	writer.Write(headers)

	// 写入数据
	for _, article := range articles {
		content := prepareCSVContent(article.Content)
		title := strings.ReplaceAll(strings.ReplaceAll(article.Title, "\"", "'"), ",", "，")

		record := []string{title, article.PublishTime, content, article.URL}
		writer.Write(record)
	}
}

// prepareCSVContent 准备CSV内容，确保格式正确
func prepareCSVContent(content string) string {
	content = strings.ReplaceAll(content, "\"", "'")  // 替换双引号为单引号
	content = strings.ReplaceAll(content, ",", "，")   // 替换半角逗号为全角逗号
	content = strings.ReplaceAll(content, "\t", " ")  // 替换制表符为空格
	return content
}

// cleanContent 清理内容并优化格式
func cleanContent(content string) string {
	// 规范化换行符
	content = strings.ReplaceAll(content, "\r\n", "\n")

	// 处理段落结构：将多个连续空行替换为两个空行
	emptyLineRegex := regexp.MustCompile(`\n{3,}`)
	content = emptyLineRegex.ReplaceAllString(content, "\n\n")

	// 处理段落内的空格
	lines := strings.Split(content, "\n")
	for i := range lines {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			spaceRegex := regexp.MustCompile(`\s{2,}`)
			lines[i] = "  " + spaceRegex.ReplaceAllString(trimmed, " ")
		}
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// convertToUTF8 转换内容编码为UTF-8
func convertToUTF8(body []byte) ([]byte, error) {
	// 尝试判断是否为UTF-8 with BOM
	if len(body) > 3 && body[0] == 0xEF && body[1] == 0xBB && body[2] == 0xBF {
		return body[3:], nil
	}

	// 尝试用GBK解码
	r := transform.NewReader(strings.NewReader(string(body)), simplifiedchinese.GBK.NewDecoder())
	utf8Body, err := ioutil.ReadAll(r)
	if err != nil {
		// 如果GBK解码失败，尝试用GB18030解码
		r = transform.NewReader(strings.NewReader(string(body)), simplifiedchinese.GB18030.NewDecoder())
		return ioutil.ReadAll(r)
	}

	return utf8Body, nil
}

// isValidUTF8 检查字符串是否是有效的UTF-8编码
func isValidUTF8(s string) bool {
	for i := 0; i < len(s); {
		if s[i] < 0x80 {
			i++
			continue
		}
		if s[i] < 0xc0 {
			return false
		}
		var n int
		if s[i] < 0xe0 {
			n = 1
		} else if s[i] < 0xf0 {
			n = 2
		} else if s[i] < 0xf8 {
			n = 3
		} else {
			return false
		}
		i++
		for j := 0; j < n; j++ {
			if i >= len(s) || s[i] < 0x80 || s[i] >= 0xc0 {
				return false
			}
			i++
		}
	}
	return true
}
