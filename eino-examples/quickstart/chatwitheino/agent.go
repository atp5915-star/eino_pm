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

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	localbk "github.com/cloudwego/eino-ext/adk/backend/local"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	commontool "github.com/cloudwego/eino-examples/adk/common/tool"
	"github.com/cloudwego/eino-examples/quickstart/chatwitheino/chatmodel"
	"github.com/cloudwego/eino-examples/quickstart/chatwitheino/helpers"
	"github.com/cloudwego/eino-examples/quickstart/chatwitheino/rag"
)

func buildAgentTyped[M adk.MessageType](ctx context.Context) (adk.TypedResumableAgent[M], error) {
	cm, err := chatmodel.NewModel[M](ctx)
	if err != nil {
		return nil, err
	}

	backend, err := localbk.NewBackend(ctx, &localbk.Config{})
	if err != nil {
		return nil, err
	}

	ragTool, err := rag.BuildTool[M](ctx, cm)
	if err != nil {
		return nil, fmt.Errorf("build rag tool: %w", err)
	}

	var handlers []adk.TypedChatModelAgentMiddleware[M]
	if skillsDir, ok := resolveSkillsDir(); ok {
		skillBackend, sbErr := skill.NewBackendFromFilesystem(ctx, &skill.BackendFromFilesystemConfig{
			Backend: backend,
			BaseDir: skillsDir,
		})
		if sbErr != nil {
			return nil, sbErr
		}
		skillMiddleware, smErr := skill.NewTyped[M](ctx, &skill.TypedConfig[M]{
			Backend:               skillBackend,
			CustomSystemPrompt:    healthSkillSystemPrompt,
			CustomToolDescription: healthSkillToolDescription,
			CustomToolParams:      healthSkillToolParams,
		})
		if smErr != nil {
			return nil, smErr
		}
		handlers = append(handlers, skillMiddleware)
	}
	handlers = append(handlers, newApprovalMiddleware[M](), helpers.NewSafeToolMiddleware[M]())

	cfg := &deep.TypedConfig[M]{
		Name:           "ChatWithEinoAgent",
		Description:    "An agent that reads and answers questions about documents.",
		ChatModel:      cm,
		Backend:        backend,
		StreamingShell: backend,
		MaxIteration:   50,
		Handlers:       handlers,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: []tool.BaseTool{ragTool},
			},
		},
	}
	helpers.ApplyMessageModelRetry(cfg)
	return deep.NewTyped[M](ctx, cfg)
}

func healthSkillSystemPrompt(_ context.Context, toolName string) string {
	return fmt.Sprintf(`
# 健康 Skill 调度规则

你有一个名为 %q 的工具，用于加载健康管家专业 Skill。只要用户的问题匹配任一健康 Skill 的触发条件，必须调用该工具，不要直接回答。

严格要求：命中 Skill 时，你的第一段 assistant content 必须是 tool_use；禁止在 tool_use 前输出任何普通文本、解释、安抚或“我先帮你分析”。

特别注意：皮肤/体表/脚部/头皮/指甲/私密部位相关症状，例如痒、越抓越痒、红、肿、疹、疱、脱皮、脚痒、皮损原因、怎么回事、怎么办等，必须调用 skin_diagnosis_v1，参数为 {"skill":"skin_diagnosis_v1"}。

特别注意：医学报告单/化验单/影像报告/病理报告/体检报告/产检报告相关解读，例如“帮我看看检查报告”“这份化验单严重吗”“CT 报告是不是肛瘘”“病理报告下一步怎么办”等，必须调用 report_reading_v1，参数为 {"skill":"report_reading_v1"}。如果当前没有真实图片，先基于用户文字说明给上传引导，不要编造图片内容。

调用 Skill 后，根据工具返回的完整 Skill 说明继续回答用户。`, toolName)
}

func healthSkillToolDescription(_ context.Context, skills []skill.FrontMatter) string {
	var builder strings.Builder
	builder.WriteString("加载并执行健康管家专业 Skill。用户请求匹配下方任一 Skill 时，必须优先调用本工具，不要直接回答。\n\n")
	builder.WriteString("可用 Skill：\n")
	for _, item := range skills {
		builder.WriteString("- ")
		builder.WriteString(item.Name)
		builder.WriteString(": ")
		builder.WriteString(strings.TrimSpace(item.Description))
		builder.WriteString("\n")
	}
	builder.WriteString("\n强制路由示例：用户说‘脚越抓越痒是什么原因？’、‘手臂红疹很痒’、‘身上起疙瘩怎么办’时，调用参数必须是 {\"skill\":\"skin_diagnosis_v1\"}。用户说‘帮我看看检查报告’、‘这份化验单严重吗’、‘CT 报告是不是肛瘘’时，调用参数必须是 {\"skill\":\"report_reading_v1\"}。")
	return builder.String()
}

func healthSkillToolParams(_ context.Context, defaults map[string]*schema.ParameterInfo) (map[string]*schema.ParameterInfo, error) {
	defaults["skill"].Desc = "要调用的健康 Skill 名称。皮肤/体表/脚部瘙痒、红疹、脱皮、皮损原因等首轮诊断问题必须填 skin_diagnosis_v1；医学报告单/化验单/影像报告/病理报告解读问题必须填 report_reading_v1。"
	defaults["skill"].Enum = []string{"skin_diagnosis_v1", "skin_collection_v1", "skin_care_plan_v1", "doctor_voice_answer_v2", "report_reading_v1"}
	return defaults, nil
}

func resolveSkillsDir() (string, bool) {
	skillsDir := strings.TrimSpace(os.Getenv("EINO_EXT_SKILLS_DIR"))
	if skillsDir == "" {
		return "", false
	}
	if absSkillsDir, absErr := filepath.Abs(skillsDir); absErr == nil {
		skillsDir = absSkillsDir
	}
	fi, err := os.Stat(skillsDir)
	if err != nil || !fi.IsDir() {
		return "", false
	}
	return skillsDir, true
}

// approvalMiddleware intercepts calls to the answer_from_document tool and
// pauses the agent with a human-approval interrupt before executing the RAG
// workflow. The runner's CheckPointStore must be configured for this to work.
type approvalMiddleware[M adk.MessageType] struct {
	*adk.TypedBaseChatModelAgentMiddleware[M]
}

func newApprovalMiddleware[M adk.MessageType]() adk.TypedChatModelAgentMiddleware[M] {
	return &approvalMiddleware[M]{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[M]{},
	}
}

// WrapInvokableToolCall inserts an approval gate around the answer_from_document
// tool. All other tools pass through unchanged.
func (m *approvalMiddleware[M]) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	tCtx *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	if tCtx.Name != "answer_from_document" {
		return endpoint, nil
	}
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		wasInterrupted, _, storedArgs := tool.GetInterruptState[string](ctx)
		if !wasInterrupted {
			return "", tool.StatefulInterrupt(ctx, &commontool.ApprovalInfo{
				ToolName:        tCtx.Name,
				ArgumentsInJSON: args,
			}, args)
		}

		isTarget, hasData, data := tool.GetResumeContext[*commontool.ApprovalResult](ctx)
		if isTarget && hasData {
			if data.Approved {
				return endpoint(ctx, storedArgs, opts...)
			}
			if data.DisapproveReason != nil {
				return fmt.Sprintf("tool '%s' disapproved: %s", tCtx.Name, *data.DisapproveReason), nil
			}
			return fmt.Sprintf("tool '%s' disapproved", tCtx.Name), nil
		}

		// Re-interrupt if this is not the resume target (another tool was resumed instead).
		isTarget, _, _ = tool.GetResumeContext[any](ctx)
		if !isTarget {
			return "", tool.StatefulInterrupt(ctx, &commontool.ApprovalInfo{
				ToolName:        tCtx.Name,
				ArgumentsInJSON: storedArgs,
			}, storedArgs)
		}

		return endpoint(ctx, storedArgs, opts...)
	}, nil
}
