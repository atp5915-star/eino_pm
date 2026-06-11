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

package a2ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/adk"

	"github.com/cloudwego/eino-examples/quickstart/chatwitheino/helpers"
	"github.com/cloudwego/eino-examples/quickstart/chatwitheino/msgops"
)

// RenderHistory writes the beginRendering + history surfaceUpdate messages to w
// without running an agent. Used to populate the chat window when a session is selected.
func RenderHistory[M adk.MessageType](w io.Writer, sessionID string, history []M) error {
	surfaceID := "chat-" + sessionID
	rootChildren := make([]string, 0, len(history))
	for i := range history {
		rootChildren = append(rootChildren, fmt.Sprintf("msg-%d-card", i))
	}
	if err := emit(w, Message{
		BeginRendering: &BeginRenderingMsg{SurfaceID: surfaceID, Root: "root-col"},
	}); err != nil {
		return err
	}
	return emitHistory(w, surfaceID, history, rootChildren)
}

// StreamToWriter converts an agent event stream into A2UI JSONL messages written to w.
// It returns the content of the last assistant text response, all intermediate messages
// (assistant tool-call messages and tool results) for session persistence,
// the interrupt ID if the agent was paused awaiting human approval (non-empty),
// the final A2UI msgIdx, and any error.
func StreamToWriter[M adk.MessageType](w io.Writer, sessionID string, history []M, events *adk.AsyncIterator[*adk.TypedAgentEvent[M]]) (string, []M, string, int, error) {
	surfaceID := "chat-" + sessionID

	rootChildren := make([]string, 0, len(history))
	for i := range history {
		rootChildren = append(rootChildren, fmt.Sprintf("msg-%d-card", i))
	}

	if err := emit(w, Message{
		BeginRendering: &BeginRenderingMsg{SurfaceID: surfaceID, Root: "root-col"},
	}); err != nil {
		return "", nil, "", 0, err
	}
	if err := emitHistory(w, surfaceID, history, rootChildren); err != nil {
		return "", nil, "", 0, err
	}

	msgIdx := len(history)
	lastContent, intermediates, interruptID, err := streamEvents(w, surfaceID, &rootChildren, &msgIdx, events)
	return lastContent, intermediates, interruptID, msgIdx, err
}

// StreamContinue resumes an interrupted stream without resetting the client UI.
// It continues from startMsgIdx, appending new chips to the existing component tree.
func StreamContinue[M adk.MessageType](w io.Writer, sessionID string, startMsgIdx int, events *adk.AsyncIterator[*adk.TypedAgentEvent[M]]) (string, string, int, error) {
	surfaceID := "chat-" + sessionID

	// Reconstruct rootChildren to match the client's current component tree.
	rootChildren := make([]string, startMsgIdx)
	for i := range rootChildren {
		rootChildren[i] = fmt.Sprintf("msg-%d-card", i)
	}

	msgIdx := startMsgIdx
	lastContent, _, interruptID, err := streamEvents(w, surfaceID, &rootChildren, &msgIdx, events)
	return lastContent, interruptID, msgIdx, err
}

// streamEvents is the shared event-processing loop used by StreamToWriter and StreamContinue.
// Returns: last assistant text content, intermediate messages (tool calls + tool results),
// interrupt ID (if any), and error.
func streamEvents[M adk.MessageType](w io.Writer, surfaceID string, rootChildren *[]string, msgIdx *int, events *adk.AsyncIterator[*adk.TypedAgentEvent[M]]) (string, []M, string, error) {
	var lastContent strings.Builder
	var interruptID string
	var intermediates []M
	iteration := 0

	_ = emitAgentTrace(w, "loop_start", "Agent loop started", map[string]any{
		"surfaceId": surfaceID,
		"model":     activeModelName(),
		"modelType": activeModelType(),
	})

	// writerBroken is set when SSE writes fail (e.g. browser aborted the
	// fetch during a preempt). When true we stop writing to the UI but keep
	// consuming events so that intermediates are fully accumulated for
	// session persistence.
	writerBroken := false

	for {
		event, ok := events.Next()
		if !ok {
			log.Printf("[a2ui] event stream ended (iterator exhausted)")
			break
		}

		if event.Err != nil {
			if helpers.IsModelRetryInProgress(event.Err) {
				log.Printf("[a2ui] model retry: %v", event.Err)
				if !writerBroken {
					_ = emitAgentTrace(w, "model_retry", "Model retry in progress", map[string]any{"error": event.Err.Error()})
					_ = emitToolChip(w, surfaceID, rootChildren, msgIdx, "retrying", event.Err.Error())
				}
				continue
			}
			log.Printf("[a2ui] event error: %v", event.Err)
			if !writerBroken {
				_ = emitAgentTrace(w, "error", "Agent event error", map[string]any{"error": event.Err.Error()})
				_ = emitToolChip(w, surfaceID, rootChildren, msgIdx, "error", event.Err.Error())
			}
			return lastContent.String(), intermediates, "", event.Err
		}

		// Detect interrupt: the agent is paused awaiting human input.
		if event.Action != nil && event.Action.Interrupted != nil {
			ictxs := event.Action.Interrupted.InterruptContexts
			var desc string
			for _, ic := range ictxs {
				if ic.IsRootCause {
					interruptID = ic.ID
					desc = fmt.Sprintf("%v", ic.Info)
					break
				}
			}
			if interruptID == "" && len(ictxs) > 0 {
				interruptID = ictxs[0].ID
				desc = fmt.Sprintf("%v", ictxs[0].Info)
			}
			log.Printf("[a2ui] interrupt: id=%s desc=%q", interruptID, desc)
			if !writerBroken {
				_ = emitAgentTrace(w, "interrupt", "Agent interrupted for approval", map[string]any{"interruptId": interruptID, "description": desc})
				_ = emitToolChip(w, surfaceID, rootChildren, msgIdx, "approval needed", desc)
				_ = emit(w, Message{
					InterruptRequest: &InterruptRequestMsg{
						InterruptID: interruptID,
						Description: desc,
					},
				})
			}
			break
		}

		hasOutput := event.Output != nil && event.Output.MessageOutput != nil
		hasExit := event.Action != nil && event.Action.Exit
		log.Printf("[a2ui] event: hasOutput=%v hasExit=%v", hasOutput, hasExit)

		if !hasOutput {
			if hasExit {
				log.Printf("[a2ui] exit (no output)")
				if !writerBroken {
					_ = emitAgentTrace(w, "loop_exit", "Agent loop exited", map[string]any{"iteration": iteration})
				}
				break
			}
			continue
		}

		iteration++
		mo := event.Output.MessageOutput
		roleLabel := msgops.VariantRoleLabel(mo)
		log.Printf("[a2ui] message output: role=%q isStreaming=%v hasStream=%v hasMessage=%v",
			roleLabel, mo.IsStreaming, mo.MessageStream != nil, !msgops.IsNil(mo.Message))
		if !writerBroken {
			_ = emitAgentTrace(w, "agent_event", "Agent event received", map[string]any{
				"iteration":  iteration,
				"role":       roleLabel,
				"streaming":  mo.IsStreaming,
				"hasStream":  mo.MessageStream != nil,
				"hasMessage": !msgops.IsNil(mo.Message),
			})
		}

		if msgops.VariantIsToolResult(mo) {
			content, toolCallID, toolName := msgops.DrainToolResult(mo)
			log.Printf("[a2ui] tool result (%d chars): %.200s", len(content), content)
			if !writerBroken {
				_ = emitAgentTrace(w, "tool_result", "Tool result received", toolResultTraceDetails(iteration, toolName, toolCallID, content))
				_ = emitToolChip(w, surfaceID, rootChildren, msgIdx, "tool result", content)
			}
			intermediates = append(intermediates, msgops.NewToolResult[M](toolCallID, toolName, content))
			continue
		}

		if mo.IsStreaming && mo.MessageStream != nil {
			textIdx := *msgIdx
			cardID := fmt.Sprintf("msg-%d-card", textIdx)
			colID := fmt.Sprintf("msg-%d-col", textIdx)
			roleID := fmt.Sprintf("msg-%d-role", textIdx)
			contentID := fmt.Sprintf("msg-%d-content", textIdx)
			dataKey := fmt.Sprintf("%s/msg-%d", surfaceID, textIdx)

			nameByIdx := map[int]string{}
			idByIdx := map[int]string{}
			argsByIdx := map[int]*strings.Builder{}
			var tcOrder []int
			seenTCIdx := map[int]bool{}

			var shellEmitted bool
			var accContent strings.Builder
			var chunks []M
			streamWillRetry := false

			for {
				chunk, recvErr := mo.MessageStream.Recv()
				if errors.Is(recvErr, io.EOF) {
					break
				}
				if recvErr != nil {
					if helpers.IsModelRetryInProgress(recvErr) {
						streamWillRetry = true
						log.Printf("[a2ui] stream retry: %v", recvErr)
						if !writerBroken {
							_ = emitToolChip(w, surfaceID, rootChildren, msgIdx, "retrying", recvErr.Error())
						}
						break
					}
					log.Printf("[a2ui] stream recv error: %v", recvErr)
					break
				}
				chunks = append(chunks, chunk)

				for _, tc := range msgops.ToolCalls(chunk) {
					idx := tc.Index
					if !seenTCIdx[idx] {
						seenTCIdx[idx] = true
						tcOrder = append(tcOrder, idx)
					}
					if tc.Name != "" && nameByIdx[idx] == "" {
						nameByIdx[idx] = tc.Name
					}
					if tc.ID != "" && idByIdx[idx] == "" {
						idByIdx[idx] = tc.ID
					}
					if tc.Args != "" {
						if argsByIdx[idx] == nil {
							argsByIdx[idx] = &strings.Builder{}
						}
						argsByIdx[idx].WriteString(tc.Args)
					}
				}

				text := msgops.AssistantDeltaText(chunk)
				if text == "" {
					continue
				}
				accContent.WriteString(text)
				if writerBroken {
					continue
				}
				if !shellEmitted {
					*rootChildren = append(*rootChildren, cardID)
					*msgIdx++
					if shellErr := emitMessageShell(w, surfaceID, *rootChildren, cardID, colID, roleID, contentID, dataKey, msgops.VariantRoleLabel(mo)); shellErr != nil {
						log.Printf("[a2ui] SSE writer broken, continuing for persistence: %v", shellErr)
						writerBroken = true
					} else {
						shellEmitted = true
					}
				}
				if !writerBroken {
					if dataErr := emitDataUpdate(w, surfaceID, dataKey, accContent.String()); dataErr != nil {
						log.Printf("[a2ui] SSE writer broken, continuing for persistence: %v", dataErr)
						writerBroken = true
					}
				}
			}
			if streamWillRetry {
				continue
			}

			var toolCalls []msgops.ToolCall
			for _, i := range tcOrder {
				name := nameByIdx[i]
				if name == "" {
					continue
				}
				args := ""
				if ab := argsByIdx[i]; ab != nil {
					args = ab.String()
				}
				toolCalls = append(toolCalls, msgops.ToolCall{
					ID:    idByIdx[i],
					Name:  name,
					Args:  args,
					Index: i,
				})
			}
			log.Printf("[a2ui] assistant stream: content=%d chars toolCalls=%d", accContent.Len(), len(toolCalls))

			if !writerBroken {
				for _, tc := range toolCalls {
					log.Printf("[a2ui] tool call: %s args=%s", tc.Name, tc.Args)
					_ = emitAgentTrace(w, "tool_call", "Tool call requested", toolCallTraceDetails(iteration, tc))
					_ = emitToolChip(w, surfaceID, rootChildren, msgIdx, "tool call", formatToolCall(tc))
				}
			}
			if shellEmitted || accContent.Len() > 0 {
				lastContent.Reset()
				lastContent.WriteString(accContent.String())
			}
			if shellEmitted || accContent.Len() > 0 || len(toolCalls) > 0 || len(chunks) > 0 {
				if merged, mergeErr := msgops.ConcatChunks(chunks); mergeErr == nil && msgops.HasContent(merged) {
					intermediates = append(intermediates, merged)
				} else if shellEmitted || accContent.Len() > 0 || len(toolCalls) > 0 {
					if mergeErr != nil {
						log.Printf("[a2ui] failed to concat stream chunks, falling back to assistant text: %v", mergeErr)
					}
					intermediates = append(intermediates, msgops.NewAssistant[M](accContent.String(), toolCalls))
				}
			}
		} else if !msgops.IsNil(mo.Message) {
			msg := mo.Message
			content := msgops.AssistantText(msg)
			toolCalls := msgops.ToolCalls(msg)
			log.Printf("[a2ui] assistant message: content=%d chars toolCalls=%d", len(content), len(toolCalls))

			if !writerBroken {
				for _, tc := range toolCalls {
					log.Printf("[a2ui] tool call: %s args=%s", tc.Name, tc.Args)
					_ = emitAgentTrace(w, "tool_call", "Tool call requested", map[string]any{
						"iteration":  iteration,
						"tool":       tc.Name,
						"toolCallId": tc.ID,
						"args":       truncateRunes(tc.Args, 500),
					})
					_ = emitToolChip(w, surfaceID, rootChildren, msgIdx, "tool call", formatToolCall(tc))
				}
				if content != "" {
					if err := emitTextCard(w, surfaceID, rootChildren, msgIdx, msgops.RoleLabel(msg), content); err != nil {
						log.Printf("[a2ui] SSE writer broken, continuing for persistence: %v", err)
						writerBroken = true
					}
				}
			}
			if content != "" {
				lastContent.Reset()
				lastContent.WriteString(content)
			}
			if content != "" || len(toolCalls) > 0 || msgops.HasContent(msg) {
				intermediates = append(intermediates, msg)
			}
		} else {
			log.Printf("[a2ui] assistant event with no stream and no message (skipped)")
		}

		if hasExit {
			log.Printf("[a2ui] exit (after output)")
			if !writerBroken {
				_ = emitAgentTrace(w, "loop_exit", "Agent loop exited", map[string]any{"iteration": iteration})
			}
			break
		}
	}

	return lastContent.String(), intermediates, interruptID, nil
}

// formatToolCall formats a function tool call for display in a chip.
func formatToolCall(tc msgops.ToolCall) string {
	text := "🔧 " + tc.Name
	if skillName := skillNameFromArgs(tc.Args); skillName != "" {
		text += "\n" + skillDisplayName(skillName)
	}
	if tc.Args != "" {
		text += "\n" + truncateRunes(tc.Args, 400)
	}
	return text
}

func toolCallTraceDetails(iteration int, tc msgops.ToolCall) map[string]any {
	details := map[string]any{
		"iteration":  iteration,
		"tool":       tc.Name,
		"toolCallId": tc.ID,
		"args":       truncateRunes(tc.Args, 500),
	}
	if skillName := skillNameFromArgs(tc.Args); skillName != "" {
		details["skill"] = skillName
		details["skillDisplayName"] = skillDisplayName(skillName)
	}
	return details
}

func toolResultTraceDetails(iteration int, toolName, toolCallID, content string) map[string]any {
	details := map[string]any{
		"iteration":  iteration,
		"tool":       toolName,
		"toolCallId": toolCallID,
		"chars":      len(content),
		"preview":    truncateRunes(content, 220),
	}
	if skillName := skillNameFromContent(content); skillName != "" {
		details["skill"] = skillName
		details["skillDisplayName"] = skillDisplayName(skillName)
	}
	return details
}

func skillNameFromArgs(args string) string {
	var payload struct {
		Skill string `json:"skill"`
	}
	if err := json.Unmarshal([]byte(args), &payload); err == nil {
		return strings.TrimSpace(payload.Skill)
	}
	return ""
}

func skillNameFromContent(content string) string {
	for skillName := range skillDisplayNames {
		if strings.Contains(content, skillName) {
			return skillName
		}
	}
	return ""
}

func skillDisplayName(skillName string) string {
	if displayName, ok := skillDisplayNames[skillName]; ok {
		return displayName
	}
	return skillName
}

var skillDisplayNames = map[string]string{
	"skin_diagnosis_v1":      "皮肤诊断与方向判断",
	"skin_collection_v1":     "皮肤病情采集",
	"skin_care_plan_v1":      "皮肤护理与治疗方案",
	"doctor_voice_answer_v2": "值班医生口吻健康问答",
	"report_reading_v1":      "医学报告单解读",
	"lung_cancer_v1":         "【管线】肺癌风险与诊疗路径",
	"weight_loss_v1":         "【管线】减肥与体重管理",
	"medical_visit_v1":       "【管线】就医决策与挂号路径",
	"maxillofacial_v1":       "【管线】颌面外科与口腔颌面问题",
	"tcm_v1":                 "【管线】中医调理与辨证建议",
	"jifeng_doctor_agent_v1": "【管线】疾风医生智能体",
	"consumer_medical_v1":    "【管线】消费医疗与健康服务决策",
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

func activeModelName() string {
	for _, key := range []string{"MESSAGES_MODEL", "OPENAI_MODEL", "OPENAI_MODEL_ID", "ARK_MODEL", "ARK_MODEL_ID"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func activeModelType() string {
	if value := strings.TrimSpace(os.Getenv("MODEL_TYPE")); value != "" {
		return value
	}
	return "chat_completions"
}

func emitAgentTrace(w io.Writer, stage, label string, details map[string]any) error {
	return emit(w, Message{
		AgentTrace: &AgentTraceMsg{
			Stage:   stage,
			Label:   label,
			Details: details,
		},
	})
}

// emitTextCard emits a text card with full content (non-streaming path).
func emitTextCard(w io.Writer, surfaceID string, rootChildren *[]string, msgIdx *int, roleLabel, content string) error {
	cleanContent, quickTests := extractQuickTests(content)
	if strings.TrimSpace(cleanContent) == "" && len(quickTests) == 0 {
		return nil
	}

	if strings.TrimSpace(cleanContent) != "" {
		idx := *msgIdx
		cardID := fmt.Sprintf("msg-%d-card", idx)
		colID := fmt.Sprintf("msg-%d-col", idx)
		roleID := fmt.Sprintf("msg-%d-role", idx)
		contentID := fmt.Sprintf("msg-%d-content", idx)
		dataKey := fmt.Sprintf("%s/msg-%d", surfaceID, idx)

		*rootChildren = append(*rootChildren, cardID)
		*msgIdx++

		if err := emitMessageShell(w, surfaceID, *rootChildren, cardID, colID, roleID, contentID, dataKey, roleLabel); err != nil {
			return err
		}
		if err := emitDataUpdate(w, surfaceID, dataKey, cleanContent); err != nil {
			return err
		}
	}

	for _, card := range quickTests {
		card.SurfaceID = surfaceID
		if err := emitQuickTest(w, &card); err != nil {
			return err
		}
	}
	return nil
}

var (
	quickReplyRe   = regexp.MustCompile(`(?s)<快捷回复>\s*(.*?)\s*</快捷回复>`)
	imageCollectRe = regexp.MustCompile(`(?s)<图片收集>\s*(.*?)\s*</图片收集>`)
)

func extractQuickTests(content string) (string, []QuickTestMsg) {
	if !strings.Contains(content, "<快捷回复>") && !strings.Contains(content, "<图片收集>") {
		return content, nil
	}

	var cards []QuickTestMsg
	matches := quickReplyRe.FindAllStringSubmatch(content, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		if card, ok := parseQuickReply(match[1]); ok {
			cards = append(cards, card)
		}
	}
	imageMatches := imageCollectRe.FindAllStringSubmatch(content, -1)
	imagePrompt := ""
	if len(imageMatches) > 0 && len(imageMatches[0]) > 1 {
		imagePrompt = strings.TrimSpace(imageMatches[0][1])
	}
	if imagePrompt != "" {
		if len(cards) == 0 {
			cards = append(cards, QuickTestMsg{Title: "补充图片信息", SelectType: "single", ImagePrompt: imagePrompt, InputOption: true})
		} else {
			cards[len(cards)-1].ImagePrompt = imagePrompt
			cards[len(cards)-1].InputOption = true
		}
	}

	cleaned := quickReplyRe.ReplaceAllString(content, "")
	cleaned = imageCollectRe.ReplaceAllString(cleaned, "")
	cleaned = strings.TrimSpace(cleaned)
	return cleaned, cards
}

func parseQuickReply(raw string) (QuickTestMsg, bool) {
	text := strings.TrimSpace(raw)
	selectType := "single"
	if strings.Contains(text, "[多选]") {
		selectType = "multiple"
	}
	text = strings.ReplaceAll(text, "[单选]", "")
	text = strings.ReplaceAll(text, "[多选]", "")
	text = strings.TrimSpace(text)

	title := text
	optionText := ""
	if before, after, ok := strings.Cut(text, ":"); ok {
		title = strings.TrimSpace(before)
		optionText = strings.TrimSpace(after)
	} else if before, after, ok := strings.Cut(text, "："); ok {
		title = strings.TrimSpace(before)
		optionText = strings.TrimSpace(after)
	}
	if title == "" {
		return QuickTestMsg{}, false
	}

	var options []string
	if optionText != "" {
		for _, item := range strings.Split(optionText, "|") {
			option := strings.TrimSpace(item)
			if option != "" {
				options = append(options, option)
			}
		}
	}
	return QuickTestMsg{Title: title, SelectType: selectType, Options: options, InputOption: true}, true
}

func emitQuickTest(w io.Writer, card *QuickTestMsg) error {
	return emit(w, Message{QuickTest: card})
}

// emitToolChip emits a compact single-line chip for tool calls or tool results.
func emitToolChip(w io.Writer, surfaceID string, rootChildren *[]string, msgIdx *int, kind, text string) error {
	idx := *msgIdx
	cardID := fmt.Sprintf("msg-%d-card", idx)
	colID := fmt.Sprintf("msg-%d-col", idx)
	labelID := fmt.Sprintf("msg-%d-label", idx)
	textID := fmt.Sprintf("msg-%d-text", idx)

	*rootChildren = append(*rootChildren, cardID)
	*msgIdx++

	// Truncate long tool output to keep the UI tidy.
	// Approval cards are never truncated — users need the full context to decide.
	display := text
	if kind != "approval needed" && len([]rune(display)) > 300 {
		display = string([]rune(display)[:300]) + "…"
	}

	return emit(w, Message{
		SurfaceUpdate: &SurfaceUpdateMsg{
			SurfaceID: surfaceID,
			Components: []Component{
				{ID: "root-col", Component: ComponentValue{Column: &ColumnComp{Children: append([]string{}, *rootChildren...)}}},
				{ID: cardID, Component: ComponentValue{Card: &CardComp{Children: []string{colID}}}},
				{ID: colID, Component: ComponentValue{Column: &ColumnComp{Children: []string{labelID, textID}}}},
				{ID: labelID, Component: ComponentValue{Text: &TextComp{Value: kind, UsageHint: "caption"}}},
				{ID: textID, Component: ComponentValue{Text: &TextComp{Value: display, UsageHint: "body"}}},
			},
		},
	})
}

func emitHistory[M adk.MessageType](w io.Writer, surfaceID string, history []M, rootChildren []string) error {
	comps := []Component{
		{ID: "root-col", Component: ComponentValue{Column: &ColumnComp{Children: append([]string{}, rootChildren...)}}},
	}
	for i, msg := range history {
		cardID := fmt.Sprintf("msg-%d-card", i)
		colID := fmt.Sprintf("msg-%d-col", i)
		roleID := fmt.Sprintf("msg-%d-role", i)
		contentID := fmt.Sprintf("msg-%d-content", i)

		label := msgops.RoleLabel(msg)
		body := msgops.Text(msg)
		if results := msgops.ToolResults(msg); len(results) > 0 {
			label = "tool result"
			body = results[0].Content
		} else if calls := msgops.ToolCalls(msg); len(calls) > 0 && body == "" {
			label = "tool call"
			body = formatToolCall(calls[0])
		}

		comps = append(comps,
			Component{ID: cardID, Component: ComponentValue{Card: &CardComp{Children: []string{colID}}}},
			Component{ID: colID, Component: ComponentValue{Column: &ColumnComp{Children: []string{roleID, contentID}}}},
			Component{ID: roleID, Component: ComponentValue{Text: &TextComp{Value: label, UsageHint: "caption"}}},
			Component{ID: contentID, Component: ComponentValue{Text: &TextComp{Value: body, UsageHint: "body"}}},
		)
	}
	return emit(w, Message{SurfaceUpdate: &SurfaceUpdateMsg{SurfaceID: surfaceID, Components: comps}})
}

func emitMessageShell(w io.Writer, surfaceID string, rootChildren []string, cardID, colID, roleID, contentID, dataKey, roleLabel string) error {
	return emit(w, Message{
		SurfaceUpdate: &SurfaceUpdateMsg{
			SurfaceID: surfaceID,
			Components: []Component{
				{ID: "root-col", Component: ComponentValue{Column: &ColumnComp{Children: append([]string{}, rootChildren...)}}},
				{ID: cardID, Component: ComponentValue{Card: &CardComp{Children: []string{colID}}}},
				{ID: colID, Component: ComponentValue{Column: &ColumnComp{Children: []string{roleID, contentID}}}},
				{ID: roleID, Component: ComponentValue{Text: &TextComp{Value: roleLabel, UsageHint: "caption"}}},
				{ID: contentID, Component: ComponentValue{Text: &TextComp{DataKey: dataKey, UsageHint: "body"}}},
			},
		},
	})
}

func emitDataUpdate(w io.Writer, surfaceID, dataKey, content string) error {
	return emit(w, Message{
		DataModelUpdate: &DataModelUpdateMsg{
			SurfaceID: surfaceID,
			Contents:  []DataContent{{Key: dataKey, ValueString: content}},
		},
	})
}

func emit(w io.Writer, msg Message) error {
	data, err := Encode(msg)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
