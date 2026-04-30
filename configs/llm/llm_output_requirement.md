只允许输出合法 JSON。

必须包含以下顶层字段：

```json
{
  "version": "1.0",
  "overall": {
    "alarm": "0",
    "alarm_task_codes": []
  },
  "task_results": [],
  "objects": []
}
```

输出规则：

1. `alarm` 只允许是字符串 `"1"` 或 `"0"`。
2. `overall.alarm` 必须等于所有 `task_results[*].alarm` 的逻辑或。
3. `overall.alarm_task_codes` 必须等于所有报警任务的 `task_code` 集合；如果没有报警，必须是 `[]`。
4. `task_results` 必须覆盖任务清单中的每一个任务，且每条至少包含：
   - `task_code`
   - `task_name`
   - `alarm`
   - `reason`
   - `object_ids`
5. `objects` 只用于 `alarm="1"` 的任务。每条至少包含：
   - `object_id`
   - `task_code`
   - `bbox2d`
   - `label`
   - `confidence`
6. 如果某任务 `alarm="1"`，必须提供 `object_ids`。
7. 如果某任务 `alarm="0"`，`object_ids` 必须为 `[]`。
8. 每个 `object_id` 必须被某个任务结果的 `object_ids` 引用，且该任务结果的 `task_code` 必须与 object 的 `task_code` 一致。
9. 同一物理目标命中多个任务时，必须拆成多条 object 记录；每条 object 只允许一个 `task_code`。
10. 不要输出 `task_mode`、`excluded`、`suggestion`、`attributes` 等可选字段，除非确实必要。

`bbox2d` 规则：

- 默认所有 object 都属于目标任务
- 使用归一化坐标，范围为 `0..1000`
- 格式固定为 `[x0, y0, x1, y1]`
- 必须满足 `x0 < x1` 且 `y0 < y1`
- 建议使用整数

最小示例：

```json
{
  "version": "1.0",
  "overall": {
    "alarm": "1",
    "alarm_task_codes": ["TASK_A"]
  },
  "task_results": [
    {
      "task_code": "TASK_A",
      "task_name": "人员闯入",
      "alarm": "1",
      "reason": "检测到人员进入目标区域",
      "object_ids": ["O001"]
    },
    {
      "task_code": "TASK_B",
      "task_name": "烟雾巡检",
      "alarm": "0",
      "reason": "未发现烟雾",
      "object_ids": []
    }
  ],
  "objects": [
    {
      "object_id": "O001",
      "task_code": "TASK_A",
      "bbox2d": [120, 180, 420, 860],
      "label": "具体的目标描述",
      "confidence": 0.92
    }
  ]
}
```
