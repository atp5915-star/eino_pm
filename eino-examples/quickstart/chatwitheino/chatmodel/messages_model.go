/*
 * Copyright 2026 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package chatmodel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type messagesModel struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
	tools   []*schema.ToolInfo
}

type messagesRequest struct {
	Model       string            `json:"model"`
	MaxTokens   int               `json:"max_tokens"`
	Temperature *float32          `json:"temperature,omitempty"`
	TopP        *float32          `json:"top_p,omitempty"`
	Stop        []string          `json:"stop_sequences,omitempty"`
	Stream      bool              `json:"stream,omitempty"`
	Messages    []messagesMessage `json:"messages"`
	Tools       []messagesTool    `json:"tools,omitempty"`
	ToolChoice  any               `json:"tool_choice,omitempty"`
}

type messagesMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
	Name    string `json:"name,omitempty"`
}

type messagesContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type messagesTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema"`
}

type messagesResponse struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Role       string                 `json:"role"`
	Content    []messagesContentBlock `json:"content"`
	StopReason string                 `json:"stop_reason"`
	Model      string                 `json:"model"`
	Usage      map[string]any         `json:"usage"`
	Error      *messagesError         `json:"error,omitempty"`
}

type messagesError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type messagesStreamEvent struct {
	Type         string               `json:"type"`
	Index        int                  `json:"index"`
	ContentBlock messagesContentBlock `json:"content_block"`
	Delta        messagesStreamDelta  `json:"delta"`
	Message      messagesResponse     `json:"message"`
}

type messagesStreamDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Thinking    string `json:"thinking"`
	PartialJSON string `json:"partial_json"`
	StopReason  string `json:"stop_reason"`
}

func newMessagesModel(apiKey, modelName, baseURL string) *messagesModel {
	return &messagesModel{
		apiKey:  apiKey,
		model:   modelName,
		baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Timeout: 3 * time.Minute,
		},
	}
}

func (m *messagesModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	options := einomodel.GetCommonOptions(nil, opts...)
	requestBody, err := m.buildRequest(input, options)
	if err != nil {
		return nil, err
	}

	response, err := m.do(ctx, requestBody)
	if err != nil {
		return nil, err
	}
	return messagesResponseToSchema(response), nil
}

func (m *messagesModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	options := einomodel.GetCommonOptions(nil, opts...)
	requestBody, err := m.buildRequest(input, options)
	if err != nil {
		return nil, err
	}
	requestBody.Stream = true
	return m.stream(ctx, requestBody)
}

func (m *messagesModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	clone := *m
	clone.tools = append([]*schema.ToolInfo(nil), tools...)
	return &clone, nil
}

func (m *messagesModel) buildRequest(input []*schema.Message, options *einomodel.Options) (*messagesRequest, error) {
	modelName := m.model
	if options.Model != nil && strings.TrimSpace(*options.Model) != "" {
		modelName = strings.TrimSpace(*options.Model)
	}
	if modelName == "" {
		return nil, fmt.Errorf("messages model is empty")
	}

	maxTokens := 4096
	if options.MaxTokens != nil && *options.MaxTokens > 0 {
		maxTokens = *options.MaxTokens
	}

	messages, err := schemaMessagesToMessages(input)
	if err != nil {
		return nil, err
	}

	tools := append([]*schema.ToolInfo(nil), m.tools...)
	if len(options.Tools) > 0 {
		tools = append(tools, options.Tools...)
	}
	requestTools, err := schemaToolsToMessagesTools(tools)
	if err != nil {
		return nil, err
	}

	request := &messagesRequest{
		Model:       modelName,
		MaxTokens:   maxTokens,
		Temperature: options.Temperature,
		TopP:        options.TopP,
		Stop:        options.Stop,
		Messages:    messages,
		Tools:       requestTools,
	}
	if options.ToolChoice != nil {
		request.ToolChoice = messagesToolChoice(*options.ToolChoice)
	}
	return request, nil
}

func (m *messagesModel) do(ctx context.Context, requestBody *messagesRequest) (*messagesResponse, error) {
	response, err := m.send(ctx, requestBody)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("messages model error, status code: %d, body: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed messagesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("messages model error: %s", parsed.Error.Message)
	}
	return &parsed, nil
}

func (m *messagesModel) send(ctx context.Context, requestBody *messagesRequest) (*http.Response, error) {
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if m.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
	return m.client.Do(request)
}

func (m *messagesModel) stream(ctx context.Context, requestBody *messagesRequest) (*schema.StreamReader[*schema.Message], error) {
	response, err := m.send(ctx, requestBody)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		body, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			return nil, readErr
		}
		return nil, fmt.Errorf("messages stream error, status code: %d, body: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	reader, writer := schema.Pipe[*schema.Message](64)
	go func() {
		defer response.Body.Close()
		defer writer.Close()
		if err := consumeMessagesStream(response.Body, writer); err != nil {
			_ = writer.Send(nil, err)
		}
	}()
	return reader, nil
}

func consumeMessagesStream(body io.Reader, writer *schema.StreamWriter[*schema.Message]) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	toolBlocks := map[int]*messagesContentBlock{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "event:") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var event messagesStreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			return err
		}
		switch event.Type {
		case "content_block_start":
			if event.ContentBlock.Type == "tool_use" {
				block := event.ContentBlock
				block.Input = nil
				toolBlocks[event.Index] = &block
			}
		case "content_block_delta":
			switch event.Delta.Type {
			case "text_delta":
				if event.Delta.Text != "" {
					if writer.Send(&schema.Message{Role: schema.Assistant, Content: event.Delta.Text}, nil) {
						return nil
					}
				}
			case "input_json_delta":
				block := toolBlocks[event.Index]
				if block != nil && event.Delta.PartialJSON != "" {
					block.Input = append(block.Input, []byte(event.Delta.PartialJSON)...)
				}
			}
		case "content_block_stop":
			block := toolBlocks[event.Index]
			if block != nil {
				message := messagesResponseToSchema(&messagesResponse{Content: []messagesContentBlock{*block}, StopReason: "tool_use"})
				if writer.Send(message, nil) {
					return nil
				}
				delete(toolBlocks, event.Index)
			}
		case "message_stop":
			return nil
		}
	}
	return scanner.Err()
}

func schemaMessagesToMessages(input []*schema.Message) ([]messagesMessage, error) {
	out := make([]messagesMessage, 0, len(input))
	for _, message := range input {
		if message == nil {
			continue
		}
		switch message.Role {
		case schema.System:
			out = append(out, messagesMessage{Role: "system", Content: messageTextContent(message)})
		case schema.User:
			out = append(out, messagesMessage{Role: "user", Content: messageTextContent(message), Name: message.Name})
		case schema.Assistant:
			content := assistantContentBlocks(message)
			if len(content) == 0 {
				content = append(content, messagesContentBlock{Type: "text", Text: messageTextContent(message)})
			}
			out = append(out, messagesMessage{Role: "assistant", Content: content, Name: message.Name})
		case schema.Tool:
			if message.ToolCallID == "" {
				continue
			}
			out = append(out, messagesMessage{
				Role: "user",
				Content: []messagesContentBlock{{
					Type:      "tool_result",
					ToolUseID: message.ToolCallID,
					Content:   messageTextContent(message),
				}},
			})
		default:
			out = append(out, messagesMessage{Role: string(message.Role), Content: messageTextContent(message), Name: message.Name})
		}
	}
	return out, nil
}

func messageTextContent(message *schema.Message) string {
	if message.Content != "" || len(message.UserInputMultiContent) == 0 {
		return message.Content
	}
	var parts []string
	for _, part := range message.UserInputMultiContent {
		if part.Type == schema.ChatMessagePartTypeText && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func assistantContentBlocks(message *schema.Message) []messagesContentBlock {
	var blocks []messagesContentBlock
	if message.Content != "" {
		blocks = append(blocks, messagesContentBlock{Type: "text", Text: message.Content})
	}
	for _, toolCall := range message.ToolCalls {
		input := json.RawMessage(toolCall.Function.Arguments)
		if len(input) == 0 || !json.Valid(input) {
			input = json.RawMessage(`{}`)
		}
		blocks = append(blocks, messagesContentBlock{
			Type:  "tool_use",
			ID:    toolCall.ID,
			Name:  toolCall.Function.Name,
			Input: input,
		})
	}
	return blocks
}

func schemaToolsToMessagesTools(tools []*schema.ToolInfo) ([]messagesTool, error) {
	out := make([]messagesTool, 0, len(tools))
	seen := map[string]bool{}
	for _, tool := range tools {
		if tool == nil || tool.Name == "" || seen[tool.Name] {
			continue
		}
		seen[tool.Name] = true
		inputSchema := any(map[string]any{"type": "object", "properties": map[string]any{}})
		if tool.ParamsOneOf != nil {
			schemaValue, err := tool.ParamsOneOf.ToJSONSchema()
			if err != nil {
				return nil, fmt.Errorf("convert tool %s schema: %w", tool.Name, err)
			}
			inputSchema = schemaValue
		}
		out = append(out, messagesTool{
			Name:        tool.Name,
			Description: tool.Desc,
			InputSchema: inputSchema,
		})
	}
	return out, nil
}

func messagesToolChoice(choice schema.ToolChoice) any {
	switch choice {
	case schema.ToolChoiceForbidden:
		return map[string]string{"type": "none"}
	case schema.ToolChoiceForced:
		return map[string]string{"type": "any"}
	default:
		return nil
	}
}

func messagesResponseToSchema(response *messagesResponse) *schema.Message {
	var textParts []string
	var toolCalls []schema.ToolCall
	for index, block := range response.Content {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)
		case "tool_use":
			arguments := string(block.Input)
			if arguments == "" {
				arguments = "{}"
			}
			idx := index
			toolCalls = append(toolCalls, schema.ToolCall{
				Index: &idx,
				ID:    block.ID,
				Type:  "function",
				Function: schema.FunctionCall{
					Name:      block.Name,
					Arguments: arguments,
				},
			})
		}
	}
	return &schema.Message{
		Role:      schema.Assistant,
		Content:   strings.Join(textParts, ""),
		ToolCalls: toolCalls,
		ResponseMeta: &schema.ResponseMeta{
			FinishReason: response.StopReason,
		},
		Extra: map[string]any{
			"id":    response.ID,
			"model": response.Model,
			"usage": response.Usage,
		},
	}
}
