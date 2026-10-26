#!/usr/bin/env python3
"""T-050 review round 1: D-096 (g) and (i), D-097 (i) in planning/decisions.json,
as in docs/DECISIONS_AND_TBD.md. Both stay proposed with approved_by and
approved_at null; nothing else changes."""
import json
import pathlib
import sys

f = pathlib.Path(sys.argv[1]) / "planning/decisions.json"
d = json.loads(f.read_text())
by = {x["id"]: x for x in d["decisions"]}

a = by["D-096"]
assert a["status"] == "proposed" and a["approved_by"] is None and a["approved_at"] is None
old_g = "(g) 有待處理的逾時（含過時與晚於 horizon 的）時 B1 不判定，B1／B2 回報 batch request 的最老成員、since 為就緒的 tick；"
new_g = ("(g) B1、B2 對 batch request 回報它的最老成員；B2 的 since 與任何 request 相同，是第一次在 tick 結束時處於 B2 狀態的 tick（D-074 (c)；"
         "G-BT18：就緒時 bundle 還在使用中，since 晚於就緒的 tick），B1 沒有被擋的 entity 時（Queued）的 since 是 batch request 建立（批次就緒）的 tick"
         "（G-BT22，只在 validator 被繞過時到得了）；待處理的 BATCH_TIMEOUT：ADR-026 D-050 B1（ACCEPTED）的文字把過時的與晚於 horizon 的都算，"
         "A（已批准的文字，已實作：G-BT19 跑到 horizon、G-BT20 在 36 分 ErrDeadlock）；B（review 第 1 輪 reviewer 與 coordinator 的建議）：晚於 horizon 的不計"
         "（G-BT19 在 7 分 ErrDeadlock），代價是與晚於 horizon 的 SERVICE_COMPLETE／DELAY_COMPLETE（D-064 (g) 仍算）不一致；C：三者晚於 horizon 的都不計；"
         "三者都只影響 validator 被繞過的模型、不改變任何鎖；建議 B，但 B 修訂已批准的 ADR-026 D-050 B1 文字，owner 明確批准前 kernel 照 A；")
assert a["proposal"].count(old_g) == 1
a["proposal"] = a["proposal"].replace(old_g, new_g)
old_i = "(i) max_wait 超過 365 天為 BATCH_INVALID（/nodes/<i>/batch/max_wait）；"
new_i = "(i) max_wait 超過 365 天為 BATCH_INVALID（/nodes/<i>/batch/max_wait），AI 的 scenario 規則與 prompts/scenario（scenario-0.7.0）也寫明；"
assert a["proposal"].count(old_i) == 1
a["proposal"] = a["proposal"].replace(old_i, new_i)
old_end = "建議照列，已實作。"
assert a["proposal"].endswith(old_end)
a["proposal"] = a["proposal"][: -len(old_end)] + "(g) 建議 B（未實作，等 owner 修訂 ADR-026 D-050 B1）；其餘建議照列，已實作。"

b = by["D-097"]
assert b["status"] == "proposed" and b["approved_by"] is None and b["approved_at"] is None
old_h = "(h) initial 等於唯一產品也是中性設定。建議照列，已實作。"
new_h = ("(h) initial 等於唯一產品也是中性設定；(i) 沒有 initial 時從未使用過的 unit 不算與任何產品相符，不比需要換線的 unit 優先"
         "（D-055 字面；review 第 1 輪 I1，reviewer 的 P3：b1 取 unit 0 並換線 7 分，不必換線的 unit 1 不被選），B：視為不必換線、排在需要換線的 unit 之前。"
         "建議照列（(i) 為 A），已實作。")
assert b["proposal"].count(old_h) == 1
b["proposal"] = b["proposal"].replace(old_h, new_h)
old_gap = "initial 等於唯一產品時是否中性。"
assert b["source_gap"].endswith(old_gap)
b["source_gap"] = b["source_gap"] + "（review 第 1 輪）沒有 initial 時從未使用過的 unit 是否優先於需要換線的 unit。"

f.write_text(json.dumps(d, indent=2, ensure_ascii=False) + "\n")
print("D-096, D-097 updated; still proposed")
