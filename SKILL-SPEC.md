# SKILL 开发规范 v1.1

> 适用于：健康管家 AI 产品策略 Demo
> 框架：Eino (Go) + OneAPI
> 协同方式：各 PM 使用 Comate 独立开发 SKILL，统一注册到主服务

---

## 一、概念说明

**SKILL** = 一个独立的策略单元，包含：
- 触发条件（什么时候被选中）
- System Prompt（模型行为指令）
- Tool 定义（可调用的工具）
- 输出协议（执行后更新什么状态）

主框架通过 **Description-Driven 路由** 自动选择合适的 SKILL：模型读取所有 SKILL 的 description，根据用户输入和当前状态决定调用哪个。

---

## 二、目录结构

每个 PM 维护自己的 SKILL 目录，统一放在项目根目录的 `skills/` 下：

```
skills/
├── {PM名}-{领域}/                    # PM 负责的领域目录
│   ├── SK-{编号}-{名称}/            # 单个 SKILL
│   │   ├── SKILL.md                 # SKILL 定义文件（必须）
│   │   └── tools/                   # Tool 实现（可选，有自定义 tool 时提供）
│   │       └── {tool_name}.go       # Go 实现
│   └── README.md                    # 领域说明（可选）
```

**示例**：
```
skills/
├── liming-skin/                     # 李明 - 皮肤科
│   ├── SK-01-diagnosis/
│   │   └── SKILL.md
│   ├── SK-02-collection/
│   │   └── SKILL.md
│   └── SK-03-treatment/
│       ├── SKILL.md
│       └── tools/
│           └── otc_drug_lookup.go
├── zhangwei-nutrition/              # 张伟 - 营养
│   ├── SK-01-diet-analysis/
│   │   └── SKILL.md
│   └── SK-02-meal-plan/
│       └── SKILL.md
└── wangfang-mental/                 # 王芳 - 心理
    └── SK-01-mood-assessment/
        └── SKILL.md
```

---

## 三、SKILL.md 文件格式

### 3.1 完整模板

```markdown
---
# ===== 元信息（必填）=====
name: diet_analysis_v1              # 唯一标识，格式: {功能}_{版本}
display_name: 饮食营养分析            # 展示名称
version: v1.0
owner: zhangwei                     # 负责人（PM 名）
domain: nutrition                   # 所属领域

# ===== 路由信息（必填）=====
description: >
  用户询问食物营养成分、热量计算、饮食搭配建议、
  或上传食物图片希望获得营养分析时触发。
  覆盖：日常饮食分析、减脂/增肌饮食规划、特定疾病饮食禁忌。
  【反例：用户问药品副作用 → 不触发，由 SK-10-kp-medicine 处理】

preconditions: null                 # 前置条件，null 表示可直接触发
# preconditions 示例:
#   - "skin_diagnosis.confidence >= 0.7"   # 需要诊断置信度达标
#   - "collection_rounds >= 1"             # 需要至少收集1轮信息

# ===== Tool 声明（选填）=====
tools:
  - name: food_nutrition_db
    description: 食物营养数据库查询，输入食物名返回营养成分表
    parameters:
      food_name:
        type: string
        required: true
        description: 食物名称
    returns:
      calories: number
      protein: number
      fat: number
      carbs: number
      fiber: number

# ===== 输出协议（必填）=====
output_schema:
  diet_type: string|null            # 饮食类型分类
  calorie_target: number|null       # 推荐热量目标
  restrictions: list                # 饮食禁忌列表
  needs_followup: boolean           # 是否需要后续跟进
---

## System Prompt

你是健康管家的营养顾问。你具备注册营养师的专业知识，同时用通俗易懂的方式和用户交流。

### 语言风格
- 字数：每轮 300-500 字
- 专业术语紧跟通俗解释：**基础代谢率（身体躺着不动一天也要消耗的热量）**
- 自称"健康管家"
- 用数据说话，给具体的量化建议

### 核心逻辑

1. 识别用户的饮食诉求类型
2. 如果用户提供了具体食物，调用 food_nutrition_db 获取数据
3. 基于数据给出分析和建议
4. 提供可执行的下一步行动

### 输出要求

- 首轮必须给出有价值的信息（不要只问问题）
- 推荐方案必须具体到食物名+份量
- 如果信息不足，在给出通用建议的同时追问关键信息

---

## Tool Call 规格

### food_nutrition_db

**输入**：
```json
{"food_name": "鸡胸肉100g"}
```

**输出**：
```json
{
  "calories": 165,
  "protein": 31,
  "fat": 3.6,
  "carbs": 0,
  "fiber": 0,
  "unit": "每100g"
}
```

---

## 对话示例

### 示例1：食物热量查询

**用户**：一碗米饭加一个煎蛋，热量多少？

**助手**：
我帮你算一下这顿饭的热量：

| 食物 | 份量 | 热量 |
|------|------|------|
| 白米饭 | 一碗(约200g) | ~230大卡 |
| 煎蛋 | 1个 | ~120大卡 |
| **合计** | | **~350大卡** |

这顿饭蛋白质偏低（约8g），建议搭配一份蔬菜（+膳食纤维）和多一个蛋白来源（如一杯豆浆约80大卡+7g蛋白）。

你是日常吃饭想控制热量，还是有具体的减脂/增肌目标？我可以给你更精准的搭配方案。
```

---

## 四、关键规则

### 4.1 路由规则

| 规则 | 说明 |
|------|------|
| description 是唯一的路由依据 | 写清楚触发场景 + 反例 |
| preconditions 是硬约束 | 不满足时路由器不会选择该 SKILL |
| 一次只激活一个 SKILL | 不支持并行多 SKILL |
| SKILL 间通过 output_schema 传递状态 | 下游 SKILL 可读取上游写入的状态 |

### 4.2 Tool 规则

| 规则 | 说明 |
|------|------|
| Tool 必须在 SKILL.md 的 tools 字段声明 | 未声明的 tool 不会注入模型 |
| Tool 实现放在 `tools/` 子目录 | 用 Go 实现，符合 Eino 的 tool.BaseTool 接口 |
| 如果 tool 暂无实现，标注 `mock: true` | 框架会返回 mock 数据 |
| 通用 tool（跨 SKILL 复用）放在 `skills/_shared/tools/` | 避免重复实现 |

### 4.3 命名规则

| 项目 | 格式 | 示例 |
|------|------|------|
| SKILL 目录 | `SK-{两位数编号}-{英文短名}` | `SK-01-diagnosis` |
| SKILL name | `{功能}_{版本}` | `skin_diagnosis_v8` |
| Tool name | `{动词}_{对象}` | `otc_drug_lookup` |
| 领域目录 | `{PM名拼音}-{领域英文}` | `liming-skin` |

### 4.4 版本管理

- SKILL.md 中的 `version` 字段标记版本
- 每次修改策略内容时递增版本号
- 重大逻辑变更建议在 SKILL.md 底部附 CHANGELOG

---

## 五、状态管理（SkillContext）

所有 SKILL 共享一个会话级状态对象。每个 SKILL 通过 `output_schema` 声明它会写入哪些字段。

### 5.1 公共字段（框架维护）

```yaml
session_id: string          # 会话ID
current_skill: string       # 当前激活的 SKILL name
turn_count: int             # 对话轮次
skill_history: list         # SKILL 调用历史 [{name, entered_at}]
```

### 5.2 领域字段（各 PM 维护）

各 PM 在自己的 output_schema 中声明字段。命名建议加领域前缀避免冲突：

```yaml
# 皮肤科
skin_scene_type: string
skin_confidence: float
skin_primary_disease: string

# 营养
nutrition_diet_type: string
nutrition_calorie_target: number

# 心理
mental_mood_score: int
mental_risk_level: string
```

### 5.3 跨 SKILL 依赖

如果你的 SKILL 需要读取其他 PM 的状态，在 preconditions 中声明：

```yaml
preconditions:
  - "skin_confidence >= 0.7"    # 依赖皮肤科诊断结果
```

---

## 六、开发流程

### 6.1 使用 Comate 开发 SKILL

1. 在 `skills/{你的目录}/` 下创建 `SK-{编号}-{名称}/` 目录
2. 创建 `SKILL.md`，按模板填写
3. 重点打磨：
   - `description`（决定路由准确性）
   - System Prompt（决定模型行为质量）
   - 对话示例（作为 few-shot）
4. 如果有自定义 tool，在 `tools/` 下实现

### 6.2 本地测试

```bash
# 在项目根目录
cd /Users/zhangtianwen/Developer/eino_pm

# 单独测试某个 SKILL（后续提供测试脚本）
go run cmd/test-skill/main.go --skill=SK-01-diagnosis --input="我脚上起了水泡很痒"
```

### 6.3 提交集成

1. 把你的 SKILL 目录提交到 `skills/` 下
2. 框架会自动扫描并注册所有 `skills/*/SK-*/SKILL.md`
3. 无需修改框架代码

---

## 七、FAQ

**Q: 我的 SKILL 需要多轮对话怎么办？**
A: 在 System Prompt 中描述多轮逻辑即可。框架会保持同一个 SKILL 激活直到路由器切换。在 output_schema 中维护你的收集状态（如 `collection_rounds`）。

**Q: 我需要调用外部 API 怎么办？**
A: 把它封装成一个 Tool。在 SKILL.md 的 tools 字段声明，在 `tools/` 目录实现。开发阶段可以标 `mock: true` 先跑通逻辑。

**Q: 如何调试路由是否正确？**
A: Demo 界面右侧有调试面板，显示当前选中的 SKILL、路由原因、状态变量。

**Q: description 怎么写才能保证路由准确？**
A: 
- 写清楚正例场景（什么情况下触发）
- 写清楚反例（什么情况不触发，应该给谁）
- 越具体越好，避免模糊泛化

**Q: 多个 SKILL 的 description 冲突了怎么办？**
A: 加反例区分。比如 A 的 description 写"【反例：xxx → 由 B 处理】"。路由模型会参考这些反例做决策。

---

## 八、输出卡片规范

SKILL 的输出不仅是文本回答，还可以包含 **推荐卡片**。每个 SKILL 可以输出以下组合：
- 仅文本回答
- 文本回答 + 卡片
- 仅卡片（无文本）

### 8.1 卡片类型

#### 1. 服务挂载小卡 (`service_mini`)

AI 回答末尾附带的轻量服务入口。

```json
{
  "card_type": "service_mini",
  "icon": "doctor_online",
  "title": "在线问医生",
  "subtitle": "根据病情推荐对症医生在线解答",
  "action": {
    "type": "link",
    "label": "问医生",
    "url": "https://..."
  }
}
```

**适用场景**：回答后需要引导用户使用某项服务（问医生、查药品、预约挂号等）

---

#### 2. 医疗服务大卡 (`service_large`)

多档位服务推荐卡，展示分级服务选项（免费/付费/专家）。

```json
{
  "card_type": "service_large",
  "header": {
    "badge": "卫健委认证医疗机构",
    "stats": "本市已服务1470万患者"
  },
  "sections": [
    {
      "title": "极速问医生·60s响应",
      "items": [
        {
          "name": "免费咨询",
          "tags": ["专属补贴"],
          "subtitle": "真人医生·15分钟一对一服务",
          "price": {"current": 0, "original": null},
          "action": {"label": "去咨询", "url": "https://..."}
        },
        {
          "name": "问三甲医生",
          "tags": [],
          "subtitle": "三甲医生服务，不满意随时退款",
          "price": {"current": 6.9, "original": 9.9},
          "action": {"label": "去咨询", "url": "https://..."}
        }
      ]
    },
    {
      "title": "权威专家·24小时沟通",
      "items": [
        {
          "name": "陈海",
          "title": "主治医师",
          "department": "泌尿外科",
          "hospital": "湖南中医药大学第一医院",
          "hospital_level": "三甲",
          "hospital_rank": "全国医院综合排行 A++++",
          "stats": {"consultations": 2294, "response_time": "30分钟内"},
          "specialties": ["胃肠炎", "幽门螺杆菌感染", "十二指杆菌感..."],
          "price": {"current": 45.0},
          "action": {"label": "去咨询", "url": "https://..."}
        }
      ]
    }
  ],
  "footer": {"label": "查看更多", "url": "https://..."}
}
```

**适用场景**：用户有明确的就医/咨询需求，需要展示服务选项

---

#### 3. 多模服务大卡 (`multimodal_large`)

引导用户进行图片/文件交互的大卡（拍照、上传等）。

```json
{
  "card_type": "multimodal_large",
  "guide": {
    "images": [
      {"label": "平整放置", "url": "guide_flat.png"},
      {"label": "完整拍摄", "url": "guide_photo.png"}
    ]
  },
  "actions": [
    {
      "icon": "camera",
      "label": "拍照",
      "action": {"type": "camera", "label": "去拍照"}
    },
    {
      "icon": "gallery",
      "label": "上传照片",
      "action": {"type": "gallery", "label": "去上传"}
    }
  ]
}
```

**适用场景**：需要用户上传图片（报告单解读、皮肤拍照、处方识别等）

---

#### 4. 服务追问气泡 (`followup_bubbles`)

AI 回答后推荐的快捷追问/服务引导气泡。

```json
{
  "card_type": "followup_bubbles",
  "bubbles": [
    {"icon": "doctor", "text": "我要在线咨询真人医生", "action": {"type": "service", "url": "https://..."}},
    {"icon": null, "text": "我这种情况需要吃什么药", "action": {"type": "query"}},
    {"icon": null, "text": "头痛可以针灸治疗吗", "action": {"type": "query"}}
  ]
}
```

**适用场景**：引导用户进一步探索（追问问题、跳转服务）

- `type: "query"` — 点击后作为新的用户输入发送
- `type: "service"` — 点击后跳转到服务页面

---

### 8.2 SKILL 中声明卡片输出

在 SKILL.md 的 frontmatter 中，通过 `output_cards` 字段声明该 SKILL 可能输出的卡片类型：

```yaml
---
name: ask_doctor_v1
# ... 其他字段 ...

output_cards:
  - type: service_large
    trigger: "用户表达想问医生/找医生/在线咨询"
  - type: followup_bubbles
    trigger: "每轮回答结束后推荐追问"
---
```

### 8.3 System Prompt 中的卡片输出指令

在 System Prompt 中告诉模型何时输出卡片，使用 JSON 标记块：

```markdown
### 卡片输出规则

当判断需要推荐服务时，在文本回答末尾输出卡片 JSON：

\```card
{"card_type": "service_mini", "icon": "doctor_online", "title": "在线问医生", ...}
\```

规则：
- 用户明确要问医生 → 输出 service_large
- 普通健康咨询回答后 → 输出 service_mini + followup_bubbles
- 需要用户拍照/上传 → 输出 multimodal_large
```

---

## 九、Mock 数据规范

开发阶段，各 PM 可以 mock 两类数据：
1. **Tool Mock** — 工具调用的模拟返回
2. **Card Mock** — 卡片内的业务数据（医生信息、价格、服务列表等）

### 9.1 目录结构

```
skills/{PM名}-{领域}/
├── SK-01-xxx/
│   ├── SKILL.md
│   └── mock/                        # Mock 数据目录
│       ├── tools/                   # Tool 返回 mock
│       │   └── {tool_name}.json     # 每个 tool 一个文件
│       └── cards/                   # 卡片数据 mock
│           └── {card_type}.json     # 每种卡片一个文件
```

### 9.2 Tool Mock 格式

文件：`mock/tools/{tool_name}.json`

```json
{
  "description": "OTC药品查询 mock 数据",
  "scenarios": [
    {
      "match": {"drug_name": "布洛芬"},
      "response": {
        "name": "布洛芬缓释胶囊",
        "generic_name": "布洛芬",
        "dosage": "0.3g/粒",
        "usage": "口服，成人一次1-2粒，一日2次",
        "contraindications": ["1岁以下婴幼儿禁用", "孕妇禁用"],
        "price_range": "8-15元/盒"
      }
    },
    {
      "match": {"drug_name": "*"},
      "response": {
        "name": "{{drug_name}}",
        "generic_name": "{{drug_name}}",
        "dosage": "常规剂量",
        "usage": "请遵医嘱",
        "contraindications": [],
        "price_range": "10-30元/盒"
      }
    }
  ]
}
```

**规则**：
- `scenarios` 是有序数组，按顺序匹配
- `match` 支持精确匹配和通配符 `*`
- `{{变量名}}` 表示使用输入参数的值
- 最后一个 scenario 建议用 `*` 兜底

### 9.3 Card Mock 格式

文件：`mock/cards/{card_type}.json`

```json
{
  "description": "医疗服务大卡 mock 数据",
  "variants": [
    {
      "id": "ask_doctor_general",
      "trigger_hint": "用户想问医生，无特定科室",
      "data": {
        "card_type": "service_large",
        "header": {
          "badge": "卫健委认证医疗机构",
          "stats": "本市已服务1470万患者"
        },
        "sections": [
          {
            "title": "极速问医生·60s响应",
            "items": [
              {
                "name": "免费咨询",
                "tags": ["专属补贴"],
                "subtitle": "真人医生·15分钟一对一服务",
                "price": {"current": 0, "original": null},
                "action": {"label": "去咨询", "url": "#mock"}
              }
            ]
          }
        ]
      }
    },
    {
      "id": "ask_doctor_dermatology",
      "trigger_hint": "用户想问皮肤科医生",
      "data": {
        "card_type": "service_large",
        "header": {"badge": "皮肤科专家团队", "stats": "累计解答10万+皮肤问题"},
        "sections": []
      }
    }
  ]
}
```

**规则**：
- `variants` 包含多个场景变体
- `id` 唯一标识该变体
- `trigger_hint` 描述什么场景下使用此变体（给开发者/测试看）
- `data` 是完整的卡片 JSON，前端可直接渲染

### 9.4 在 SKILL.md 中引用 Mock

```yaml
tools:
  - name: otc_drug_lookup
    description: OTC药品查询
    mock: true                       # 标记使用 mock
    mock_file: mock/tools/otc_drug_lookup.json   # 可选，默认按名称查找

output_cards:
  - type: service_large
    mock: true
    mock_file: mock/cards/service_large.json
```

### 9.5 Mock 开关

- 开发阶段：`MOCK_MODE=true`（环境变量），所有 tool 和 card 使用 mock 数据
- 联调阶段：可单独对某些 tool 关闭 mock（在 SKILL.md 中设 `mock: false`）
- 演示阶段：使用真实模型 + mock tool 数据（保证 demo 稳定可控）
