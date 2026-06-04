# SKILL 开发上手指南

> 给各位 PM 的快速使用说明
> 最后更新：2026-06-04

---

## 你需要做什么

用 Comate 对话，生成一个 SKILL 目录，打包发给我。就这么简单。

---

## 第一步：让 Comate 读规范

打开 Comate，发送：

```
帮我阅读这个文件：/Users/zhangtianwen/Developer/eino_pm/SKILL-SPEC.md
然后按照里面的规范，帮我创建一个 SKILL。
```

如果你的 Comate 访问不到这个路径，我会把 `SKILL-SPEC.md` 发给你，放在你自己的工作目录下让 Comate 读。

---

## 第二步：告诉 Comate 你要做什么策略

示例对话：

```
我要创建一个"营养饮食分析"的 SKILL，功能是：
- 用户问食物热量、饮食搭配时触发
- 能调用食物营养数据库查询工具
- 回答后推荐"服务追问气泡"（比如"需要定制减脂食谱吗"）

请按 SKILL-SPEC.md 规范生成完整的 SKILL.md 和 mock 数据。
```

Comate 会帮你生成：
- `SKILL.md`（策略定义文件）
- `mock/tools/xxx.json`（工具 mock 数据）
- `mock/cards/xxx.json`（卡片 mock 数据）

---

## 第三步：检查输出结构

确保你的目录长这样：

```
SK-01-你的功能名/
├── SKILL.md              ← 必须有
└── mock/                 ← 有 mock 数据时提供
    ├── tools/
    │   └── xxx.json
    └── cards/
        └── xxx.json
```

---

## 第四步：打包发我

把整个 `SK-01-xxx/` 目录压缩成 zip，飞书/微信发给我即可。

命名格式：`{你的名字}-{领域}-SK-{编号}.zip`

示例：`张伟-营养-SK-01.zip`

---

## SKILL.md 长什么样（最小示例）

```markdown
---
name: diet_analysis_v1
display_name: 饮食营养分析
version: v1.0
owner: zhangwei
domain: nutrition

description: >
  用户询问食物热量、营养成分、饮食搭配建议时触发。
  【反例：用户问药品副作用 → 不触发，由药品科普 SKILL 处理】

preconditions: null

tools:
  - name: food_nutrition_db
    description: 食物营养数据库查询
    mock: true

output_cards:
  - type: followup_bubbles
    trigger: "回答后推荐追问"

output_schema:
  diet_type: string|null
  needs_followup: boolean
---

## System Prompt

你是健康管家的营养顾问。用通俗易懂的方式帮用户分析饮食。

### 核心规则
- 首轮必须给出有价值的信息，不要只问问题
- 给具体数据（热量、克数）
- 每轮 300-500 字

### 卡片输出规则
回答结束后，输出追问气泡：
\```card
{"card_type": "followup_bubbles", "bubbles": [{"text": "帮我做一周减脂食谱", "action": {"type": "query"}}]}
\```

---

## 对话示例

**用户**：一碗米饭多少热量

**助手**：一碗白米饭（约200g）大约 230 大卡...
```

---

## 关于卡片

你的 SKILL 可以输出 4 种卡片，按需选择：

| 卡片 | 类型标识 | 什么时候用 |
|------|---------|-----------|
| 服务挂载小卡 | `service_mini` | 回答末尾轻量推荐（如"问医生"按钮） |
| 医疗服务大卡 | `service_large` | 推荐分级服务（免费/付费/专家列表） |
| 多模服务大卡 | `multimodal_large` | 引导用户拍照/上传 |
| 服务追问气泡 | `followup_bubbles` | 推荐追问问题或跳转服务 |

不需要所有都用，按你的策略场景选。具体 JSON 格式见规范文件第八章。

---

## 关于 Mock 数据

开发阶段所有工具调用和卡片数据都用 mock。你只需要准备：

- **Tool mock**：你的工具会返回什么数据？写几个典型场景
- **Card mock**：卡片里显示什么内容？写 1-2 个变体

格式见规范文件第九章，或者直接让 Comate 生成。

---

## 常见问题

**Q：我不会写代码怎么办？**
A：不需要写代码。全程用 Comate 对话生成 SKILL.md 和 mock JSON 文件。

**Q：description 怎么写才好？**
A：写清楚两件事：什么场景触发 + 什么场景不触发（反例）。越具体越好。

**Q：我想让模型追问用户怎么办？**
A：在 System Prompt 里写清楚追问逻辑。比如"如果用户没说年龄，先问年龄再给方案"。

**Q：我的 SKILL 需要用到别人的诊断结果怎么办？**
A：在 preconditions 里声明依赖，比如 `"skin_confidence >= 0.7"`。告诉我，我来配置流转关系。

**Q：我可以先 mock 整个对话吗？**
A：可以。在对话示例里多写几轮典型对话，这也是验证策略逻辑的好方式。

---

## 时间节点

请在开发完成后把 zip 包发给我，我统一集成到 Demo 里。有任何问题随时问我。
