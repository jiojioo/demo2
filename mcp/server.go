package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	SMTPHost    = "smtp.126.com"
	SMTPPort    = "25" // 或 "587" 用于 StartTLS
	SenderEmail = "your emaii"
	SenderAuth  = "your " // 请确认授权码正确
)

func main() {
	mcpServer := server.NewMCPServer("weather-email", mcp.LATEST_PROTOCOL_VERSION)
	mcpServer.AddTool(WeatherTool(), getWeatherHandler)
	mcpServer.AddTool(EmailTool(), sendEmailHandler)

	fmt.Println("MCP 服务启动：http://localhost:12345/sse")
	err := server.NewSSEServer(mcpServer).Start("localhost:12345")
	if err != nil {
		panic(err)
	}
}

func WeatherTool() mcp.Tool {
	return mcp.NewTool("get_weather",
		mcp.WithDescription("查询城市天气"),
		mcp.WithString("city", mcp.Required()),
		mcp.WithString("extensions", mcp.DefaultString("base")),
	)
}

func getWeatherHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	params := request.GetArguments()
	city, ok := params["city"].(string)
	if !ok {
		return nil, fmt.Errorf("缺少city")
	}
	ext, _ := params["extensions"].(string)
	if ext == "" {
		ext = "base"
	}

	u := "https://restapi.amap.com/v3/weather/weatherInfo"
	q := url.Values{}
	q.Set("city", city)
	q.Set("key", "your key")
	q.Set("extensions", ext)
	q.Set("output", "JSON")

	resp, err := http.Get(u + "?" + q.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return mcp.NewToolResultText(string(b)), nil
}

func EmailTool() mcp.Tool {
	return mcp.NewTool("send_email",
		mcp.WithDescription("发送邮件（支持HTML格式）"),
		mcp.WithString("to", mcp.Required()),
		mcp.WithString("subject", mcp.Required()),
		mcp.WithString("content", mcp.Required()),
		mcp.WithBoolean("html", mcp.DefaultBool(false)),
	)
}

func sendEmailHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// 参数获取
	params := request.GetArguments()
	to, ok1 := params["to"].(string)
	subject, ok2 := params["subject"].(string)
	content, ok3 := params["content"].(string)
	html, _ := params["html"].(bool)

	if !ok1 || !ok2 || !ok3 {
		return nil, fmt.Errorf("缺少必要的邮件参数")
	}

	// 验证收件人邮箱格式
	if !strings.Contains(to, "@") {
		return nil, fmt.Errorf("收件人邮箱格式不正确")
	}

	// 构建邮件内容
	var contentType string
	if html {
		contentType = "text/html; charset=utf-8"
	} else {
		contentType = "text/plain; charset=utf-8"
	}

	msg := fmt.Sprintf(
		"From: %s\r\n"+
			"To: %s\r\n"+
			"Subject: =?UTF-8?B?%s?=\r\n"+ // UTF-8编码的Subject
			"Content-Type: %s\r\n"+
			"MIME-Version: 1.0\r\n"+
			"\r\n%s",
		SenderEmail, to, base64.StdEncoding.EncodeToString([]byte(subject)), contentType, content,
	)

	err := sendEmailWithStartTLS(to, []byte(msg))
	if err != nil {
		// 如果StartTLS失败，尝试SSL
		err = sendEmailWithSSL(to, []byte(msg))
		if err != nil {
			return nil, fmt.Errorf("邮件发送失败: %v", err)
		}
	}

	return mcp.NewToolResultText("✅ 邮件发送成功！"), nil
}

func sendEmailWithStartTLS(to string, msg []byte) error {
	// 连接到SMTP服务器
	client, err := smtp.Dial(SMTPHost + ":" + SMTPPort)
	if err != nil {
		return fmt.Errorf("连接SMTP服务器失败: %v", err)
	}
	defer client.Close()

	// 发送EHLO
	if err = client.Hello("localhost"); err != nil {
		return fmt.Errorf("EHLO失败: %v", err)
	}

	// 开启TLS
	tlsConfig := &tls.Config{
		ServerName:         SMTPHost,
		InsecureSkipVerify: false,
	}
	if err = client.StartTLS(tlsConfig); err != nil {
		return fmt.Errorf("StartTLS失败: %v", err)
	}

	// 认证
	auth := smtp.PlainAuth("", SenderEmail, SenderAuth, SMTPHost)
	if err = client.Auth(auth); err != nil {
		return fmt.Errorf("认证失败: %v", err)
	}

	// 设置发件人
	if err = client.Mail(SenderEmail); err != nil {
		return fmt.Errorf("设置发件人失败: %v", err)
	}

	// 设置收件人
	if err = client.Rcpt(to); err != nil {
		return fmt.Errorf("设置收件人失败: %v", err)
	}

	// 发送邮件内容
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("准备发送数据失败: %v", err)
	}
	defer w.Close()

	_, err = w.Write(msg)
	return err
}

func sendEmailWithSSL(to string, msg []byte) error {
	tlsConfig := &tls.Config{
		ServerName:         SMTPHost,
		InsecureSkipVerify: false,
	}

	conn, err := tls.Dial("tcp", SMTPHost+":465", tlsConfig)
	if err != nil {
		return fmt.Errorf("SSL连接失败: %v", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, SMTPHost)
	if err != nil {
		return fmt.Errorf("创建SMTP客户端失败: %v", err)
	}
	defer client.Quit()

	auth := smtp.PlainAuth("", SenderEmail, SenderAuth, SMTPHost)
	if err = client.Auth(auth); err != nil {
		return fmt.Errorf("认证失败: %v", err)
	}

	if err = client.Mail(SenderEmail); err != nil {
		return err
	}
	if err = client.Rcpt(to); err != nil {
		return err
	}

	w, err := client.Data()
	if err != nil {
		return err
	}
	defer w.Close()

	_, err = w.Write(msg)
	return err
}
