package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	mcpTool "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func main() {
	ctx := context.Background()

	// 1. 初始化通义千问大模型
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		APIKey:  os.Getenv("DASHSCOPE_API_KEY"),
		Model:   "qwen3.5-plus",
	})
	if err != nil {
		log.Fatalf("模型初始化失败: %v", err)
	}

	// 2. 获取MCP工具（自动加载：天气 + 邮件）
	mcpTools, err := getMcpTools()
	if err != nil {
		log.Fatalf("获取MCP工具失败: %v", err)
	}

	// 3. 构建ReAct智能体（流式输出 + 双工具）
	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: mcpTools,
		},
		// ===================== 核心：更新提示词 =====================
		MessageModifier: func(ctx context.Context, input []*schema.Message) []*schema.Message {
			sysMsg := &schema.Message{
				Role: schema.System,
				Content: `你是智能助手，支持3种能力：
1. 普通聊天：直接回答，不调用工具
2. 查询天气：自动调用get_weather工具，禁止编造
3. 发送邮件：自动调用send_email工具，信息不全会主动询问用户`,
			}
			return append([]*schema.Message{sysMsg}, input...)
		},
		MaxStep: 3,
	})
	if err != nil {
		log.Fatalf("Agent初始化失败: %v", err)
	}

	// 4. 交互式流式对话
	runInteractiveAgent(agent)
}

// getMcpTools 连接MCP服务器（无修改）
func getMcpTools() ([]tool.BaseTool, error) {
	ctx := context.Background()
	fmt.Println("正在连接 MCP 服务器：http://localhost:12345/sse...")
	cli, err := client.NewSSEMCPClient("http://localhost:12345/sse")
	if err != nil {
		return nil, fmt.Errorf("创建MCP客户端失败: %v", err)
	}

	err = cli.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("启动MCP客户端失败: %v", err)
	}
	fmt.Println("✅ MCP 客户端连接成功")

	initializeRequest := mcp.InitializeRequest{}
	initializeRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initializeRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "eino-mcp-client",
		Version: "1.0.0",
	}
	_, err = cli.Initialize(ctx, initializeRequest)
	if err != nil {
		return nil, fmt.Errorf("MCP初始化失败: %v", err)
	}

	tools, err := mcpTool.GetTools(ctx, &mcpTool.Config{Cli: cli})
	if err != nil {
		return nil, fmt.Errorf("获取MCP工具失败: %v", err)
	}
	fmt.Printf("✅ 成功加载 MCP 工具数量: %d\n", len(tools))
	fmt.Println("\n=== 智能对话助手已启动（输入 exit 退出）===")
	return tools, nil
}

// runInteractiveAgent 流式输出交互（无修改）
func runInteractiveAgent(agent *react.Agent) {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\n你：")
		if !scanner.Scan() {
			fmt.Println("输入异常，程序退出")
			return
		}

		input := strings.TrimSpace(scanner.Text())
		switch strings.ToLower(input) {
		case "exit", "quit":
			fmt.Println("助手：再见！")
			return
		}
		if input == "" {
			fmt.Println("助手：请输入你想询问的内容~")
			continue
		}

		msg := []*schema.Message{schema.UserMessage(input)}

		// 流式生成回复
		stream, err := agent.Stream(context.Background(), msg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "错误：", err)
			continue
		}
		defer stream.Close()

		fmt.Print("助手：")
		for {
			frame, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "流式输出错误：", err)
				break
			}
			if frame != nil {
				fmt.Fprint(os.Stdout, frame.Content)
			}
		}
		fmt.Println()
	}
}
