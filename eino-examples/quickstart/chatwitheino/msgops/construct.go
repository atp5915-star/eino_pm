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

package msgops

import (
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// ToolCall contains the common function-tool-call fields used by the examples.
type ToolCall struct {
	ID    string
	Name  string
	Args  string
	Index int
}

// ToolResult contains the common function-tool-result fields used by the examples.
type ToolResult struct {
	ID      string
	Name    string
	Content string
}

// NewUser constructs a user message for M.
func NewUser[M adk.MessageType](text string) M {
	return NewUserWithImages[M](text, nil)
}

// ImageInput contains an image URL or base64 payload for multimodal user messages.
type ImageInput struct {
	URL        string
	Base64Data string
	MIMEType   string
}

// NewUserWithImages constructs a user message with optional image URLs for M.
func NewUserWithImages[M adk.MessageType](text string, imageURLs []string) M {
	images := make([]ImageInput, 0, len(imageURLs))
	for _, imageURL := range imageURLs {
		images = append(images, ImageInput{URL: imageURL})
	}
	return NewUserWithImageInputs[M](text, images)
}

// NewUserWithImageInputs constructs a user message with optional image inputs for M.
func NewUserWithImageInputs[M adk.MessageType](text string, images []ImageInput) M {
	if KindOf[M]() == KindAgentic {
		blocks := []*schema.ContentBlock{schema.NewContentBlock(&schema.UserInputText{Text: text})}
		for _, image := range images {
			blocks = append(blocks, schema.NewContentBlock(&schema.UserInputImage{
				URL:        image.URL,
				Base64Data: image.Base64Data,
				MIMEType:   image.MIMEType,
				Detail:     schema.ImageURLDetailAuto,
			}))
		}
		return any(&schema.AgenticMessage{
			Role:          schema.AgenticRoleTypeUser,
			ContentBlocks: blocks,
		}).(M)
	}

	msg := schema.UserMessage(text)
	if len(images) > 0 {
		msg.Content = ""
		msg.UserInputMultiContent = []schema.MessageInputPart{{
			Type: schema.ChatMessagePartTypeText,
			Text: text,
		}}
		for _, image := range images {
			part := schema.MessageInputPart{
				Type: schema.ChatMessagePartTypeImageURL,
				Image: &schema.MessageInputImage{
					MessagePartCommon: schema.MessagePartCommon{MIMEType: image.MIMEType},
					Detail:            schema.ImageURLDetailAuto,
				},
			}
			if image.Base64Data != "" {
				base64Data := image.Base64Data
				part.Image.Base64Data = &base64Data
			} else {
				url := image.URL
				part.Image.URL = &url
			}
			msg.UserInputMultiContent = append(msg.UserInputMultiContent, part)
		}
	}
	return any(msg).(M)
}

// NewSystem constructs a system message for M.
func NewSystem[M adk.MessageType](text string) M {
	if KindOf[M]() == KindAgentic {
		return any(schema.SystemAgenticMessage(text)).(M)
	}
	return any(schema.SystemMessage(text)).(M)
}

// NewAssistant constructs an assistant message with optional function tool calls.
func NewAssistant[M adk.MessageType](text string, calls []ToolCall) M {
	if KindOf[M]() == KindAgentic {
		blocks := make([]*schema.ContentBlock, 0, len(calls)+1)
		if text != "" {
			blocks = append(blocks, normalizeAgenticContentBlock(schema.NewContentBlock(&schema.AssistantGenText{Text: text})))
		}
		for _, call := range calls {
			blocks = append(blocks, normalizeAgenticContentBlock(schema.NewContentBlock(&schema.FunctionToolCall{
				CallID:    call.ID,
				Name:      call.Name,
				Arguments: call.Args,
			})))
		}
		return any(&schema.AgenticMessage{
			Role:          schema.AgenticRoleTypeAssistant,
			ContentBlocks: blocks,
		}).(M)
	}

	schemaCalls := make([]schema.ToolCall, 0, len(calls))
	for _, call := range calls {
		idx := call.Index
		schemaCalls = append(schemaCalls, schema.ToolCall{
			ID:       call.ID,
			Index:    &idx,
			Function: schema.FunctionCall{Name: call.Name, Arguments: call.Args},
		})
	}
	return any(schema.AssistantMessage(text, schemaCalls)).(M)
}

// NewToolResult constructs a tool-result message for M.
func NewToolResult[M adk.MessageType](id, name, content string) M {
	if KindOf[M]() == KindAgentic {
		var blocks []*schema.FunctionToolResultContentBlock
		if content != "" {
			blocks = []*schema.FunctionToolResultContentBlock{{
				Type: schema.FunctionToolResultContentBlockTypeText,
				Text: &schema.UserInputText{Text: content},
			}}
		}
		return any(&schema.AgenticMessage{
			Role: schema.AgenticRoleTypeUser,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.FunctionToolResult{
					CallID:  id,
					Name:    name,
					Content: blocks,
				}),
			},
		}).(M)
	}
	return any(schema.ToolMessage(content, id, schema.WithToolName(name))).(M)
}
