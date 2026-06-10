#!/usr/bin/env python3
"""
eval_skill.py — 跑 SK-01-report-reading 在 18 case 上的"压案例 vs 理想态"对比

流程：
  1. 解析 HTML 抽 18 case（query, ideal md）
  2. 每个 case 注入 OCR mock（按关键字命中）+ SP，调 LLM 生成候选回复
  3. judge LLM 对 候选 vs 理想态 评 0-100（结构/结论/语气/行动 四维 + 总分）
  4. 输出 markdown 报告，列每个 case 的分数、低分项理由

用法：
  export LLM_API_KEY=...
  export LLM_BASE_URL=https://oneapi-comate.baidu-int.com/v1
  export LLM_MODEL=gpt-5.5
  python eval_skill.py --round 1
"""
import argparse, json, os, re, ssl, sys, time, urllib.request, urllib.error
from pathlib import Path

ROOT = Path("/Users/zhoujiyi/ComateProjects/comate-zulu-demo")
SKILL_DIR = ROOT / "skills/zhoujiyi-report/SK-01-report-reading"
HTML = ROOT / "报告单_豆包vs阿福_横评.html"
OUT_DIR = ROOT / "skills/zhoujiyi-report/SK-01-report-reading/eval_runs"
OUT_DIR.mkdir(exist_ok=True, parents=True)

# 每个 case 对应的 OCR mock 关键字（命中 mock/tools/report_ocr_extract.json 的 match.image_url）
CASE_OCR_KEY = {
    1: "糖耐", 2: "产后", 3: "腹部CT", 4: "病理 TGCT",
    5: "吞咽", 6: "尿 微白蛋白", 7: "肛瘘", 8: "ALK",
    9: "eGFR", 10: "胆囊息肉", 11: "EB VCA", 12: "PTC",
    13: "胃镜", 14: "白带", 15: "血脂", 16: "PET 鼻咽",
    17: "肋骨骨折", 18: "激素六项",
}

# ---------- 工具 ----------
def parse_skill_md():
    md = (SKILL_DIR / "SKILL.md").read_text(encoding="utf-8")
    m = re.match(r"^---\n.*?\n---\n(.*)$", md, re.S)
    body = m.group(1) if m else md
    sp = re.search(r"## System Prompt\n(.*?)(?=\n## 对话示例|\Z)", body, re.S)
    examples = re.search(r"## 对话示例.*?\n(.*?)(?=\n## CHANGELOG|\Z)", body, re.S)
    return (sp.group(1) if sp else body).strip(), (examples.group(1) if examples else "").strip()

def load_ocr_mock():
    return json.loads((SKILL_DIR / "mock/tools/report_ocr_extract.json").read_text(encoding="utf-8"))

def match_ocr(scenarios, key):
    for sc in scenarios:
        m = sc["match"]["image_url"]
        if m == "*":
            return sc["response"]
        # match.image_url 形如 "*糖耐*"，把空格转成 .* 后再匹配
        pat = re.escape(m).replace(r"\*", ".*").replace(r"\ ", ".*")
        if re.search(pat, key):
            return sc["response"]
    return scenarios[-1]["response"]

def parse_cases():
    """从 HTML 抽 18 case 的 query / intent / severity + IDEAL.md"""
    txt = HTML.read_text(encoding="utf-8")
    cases_m = re.search(r"const CASES = (\[.*?\]);", txt, re.S).group(1)
    cases = json.loads(cases_m)

    # IDEAL = { 1: {tag, md}, ... }
    ideal_block = re.search(r"const IDEAL = \{(.*?)\n\};\n", txt, re.S).group(1)
    # 用按 id 分块的方式抓 — md 里有 ` 反引号
    ideals = {}
    for m in re.finditer(r"\n  (\d+): \{\n\s*tag: \"(.*?)\",\n\s*md: `(.*?)`\n  \}", ideal_block, re.S):
        cid = int(m.group(1))
        ideals[cid] = {"tag": m.group(2), "md": m.group(3)}

    out = []
    for c in cases:
        cid = c["id"]
        out.append({
            "id": cid,
            "intent": c["intent"],
            "severity": c["severity"],
            "query": c["query"],
            "ideal_tag": ideals[cid]["tag"],
            "ideal_md": ideals[cid]["md"],
        })
    return out

# ---------- LLM 调用 ----------
SSL_CTX = ssl.create_default_context()
try:
    import certifi
    SSL_CTX = ssl.create_default_context(cafile=certifi.where())
except Exception:
    pass

def call_llm(api_key, base_url, model, system, user, max_retries=2):
    url = base_url.rstrip("/") + "/chat/completions"
    payload = {
        "model": model,
        "messages": [{"role": "system", "content": system}, {"role": "user", "content": user}],
        "temperature": 0.5,
    }
    last = None
    for attempt in range(max_retries + 1):
        req = urllib.request.Request(url, data=json.dumps(payload).encode("utf-8"),
                                     headers={"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=180, context=SSL_CTX) as r:
                data = json.loads(r.read().decode("utf-8"))
                return data["choices"][0]["message"]["content"]
        except urllib.error.HTTPError as e:
            last = f"HTTP {e.code}: {e.read().decode('utf-8', 'replace')[:200]}"
        except Exception as e:
            last = str(e)
        time.sleep(2)
    raise RuntimeError(f"LLM 调用失败: {last}")

# ---------- 主逻辑 ----------
def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--round", type=int, default=1)
    ap.add_argument("--ids", type=str, default="", help="逗号分隔 case id；为空则全跑")
    args = ap.parse_args()

    api_key = os.environ.get("LLM_API_KEY")
    base_url = os.environ.get("LLM_BASE_URL", "https://oneapi-comate.baidu-int.com/v1")
    model = os.environ.get("LLM_MODEL", "gpt-5.5")
    if not api_key:
        sys.exit("LLM_API_KEY 未设置")

    sp, examples = parse_skill_md()
    ocr_data = load_ocr_mock()
    cases = parse_cases()
    if args.ids:
        ids = {int(x) for x in args.ids.split(",")}
        cases = [c for c in cases if c["id"] in ids]

    results = []
    for c in cases:
        cid = c["id"]
        ocr_resp = match_ocr(ocr_data["scenarios"], CASE_OCR_KEY[cid])

        # 构造增强 system：原 SP + 工具结果注入 + few-shot
        system = sp + "\n\n## 已为你预调用的工具结果（基于这些数据组织回复）\n```json\n" + \
                 json.dumps(ocr_resp, ensure_ascii=False, indent=2) + "\n```"
        if examples:
            system += "\n\n## 风格参考（few-shot）\n" + examples

        # 构造 user：图片 + query
        user = f"[用户上传报告单图片，OCR 关键字：{CASE_OCR_KEY[cid]}]\n用户提问：{c['query']}"

        print(f"=== case {cid:02d}  {c['intent']}/{c['severity']}  query={c['query'][:30]!r} ===", flush=True)
        try:
            cand = call_llm(api_key, base_url, model, system, user)
        except Exception as e:
            print(f"  ERROR: {e}", flush=True)
            results.append({**c, "candidate": "", "error": str(e), "score": None})
            continue

        # judge
        judge_sys = """你是医疗 AI 输出质检员。给定一份「理想态回复」和「候选回复」，请从 4 个维度 0-100 打分：
1. 结构 (structure)：是否核心结论前置 + 分块清晰
2. 结论 (conclusion)：核心判断/严重度定调是否准确、安抚力度匹配
3. 语气 (tone)：温度感（先稳住-后专业）、术语是否带白话
4. 行动 (action)：药名/剂量/复查/红线 是否具体可落地
输出 JSON：{"structure":xx,"conclusion":xx,"tone":xx,"action":xx,"avg":xx,"weak":["低分项简评(<=80字)"]}"""
        judge_user = f"【理想态】\n{c['ideal_md']}\n\n【候选】\n{cand}"
        try:
            jr = call_llm(api_key, base_url, model, judge_sys, judge_user)
            jm = re.search(r"\{.*\}", jr, re.S)
            score = json.loads(jm.group(0)) if jm else {"raw": jr}
        except Exception as e:
            score = {"error": str(e)}

        avg = score.get("avg") if isinstance(score, dict) else None
        print(f"  -> avg={avg}  weak={score.get('weak') if isinstance(score, dict) else '?'}", flush=True)
        results.append({**c, "candidate": cand, "score": score})
        time.sleep(0.3)

    # 输出
    out_json = OUT_DIR / f"round{args.round}.json"
    out_json.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"\n[saved] {out_json}")

    # 汇总
    avgs = [r["score"]["avg"] for r in results if isinstance(r.get("score"), dict) and "avg" in r["score"]]
    if avgs:
        print(f"[summary] N={len(avgs)}  mean_avg={sum(avgs)/len(avgs):.1f}  min={min(avgs)}  max={max(avgs)}")
        print("[low cases]", sorted([(r['id'], r['score']['avg']) for r in results if isinstance(r.get('score'), dict) and r['score'].get('avg', 100) < 85]))


if __name__ == "__main__":
    main()
