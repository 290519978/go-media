请仅返回 JSON，不要输出任何额外说明。JSON 结构固定如下：

```json
{
  "alarm": "0",
  "reason": "整体判定依据，简明扼要",
  "anomaly_times": [
    {
      "timestamp_ms": 12000,
      "timestamp_text": "00:12",
      "reason": "该时刻出现的异常现象"
    }
  ]
}
```

要求：
1. `alarm` 只能是 `"0"` 或 `"1"`。
2. `reason` 必须是整体结论的判定依据。
3. `anomaly_times` 必须返回全部异常时间点或时间段；若未发现异常则返回空数组。
4. `timestamp_ms` 必须是整数毫秒；`timestamp_text` 使用 `MM:SS` 或 `HH:MM:SS`。
