package server

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	gopsnet "github.com/shirou/gopsutil/v4/net"
)

type runtimeMemoryUsage struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
}

type runtimeDiskUsage struct {
	Path        string  `json:"path"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
}

type runtimeNetworkUsage struct {
	RXBPS   float64 `json:"rx_bps"`
	TXBPS   float64 `json:"tx_bps"`
	RXBytes uint64  `json:"rx_bytes"`
	TXBytes uint64  `json:"tx_bytes"`
}

type runtimeGPUUsage struct {
	Supported     bool    `json:"supported"`
	UtilPercent   float64 `json:"util_percent"`
	MemoryUsedMB  float64 `json:"memory_used_mb"`
	MemoryTotalMB float64 `json:"memory_total_mb"`
	Message       string  `json:"message"`
}

type runtimeMetricsPayload struct {
	Timestamp     int64               `json:"timestamp"`
	Version       string              `json:"version"`
	UptimeSeconds int64               `json:"uptime_seconds"`
	CPUPercent    float64             `json:"cpu_percent"`
	Memory        runtimeMemoryUsage  `json:"memory"`
	Disk          runtimeDiskUsage    `json:"disk"`
	Network       runtimeNetworkUsage `json:"network"`
	GPU           runtimeGPUUsage     `json:"gpu"`
}

func (s *Server) collectRuntimeMetrics(now time.Time) runtimeMetricsPayload {
	cpuPercents, _ := cpu.Percent(0, false)
	vm, _ := mem.VirtualMemory()
	cwd, _ := os.Getwd()
	du, _ := disk.Usage(cwd)
	cpuPercent := 0.0
	if len(cpuPercents) > 0 {
		cpuPercent = cpuPercents[0]
	}

	uptimeSeconds := int64(0)
	if !s.startedAt.IsZero() {
		uptimeSeconds = int64(now.Sub(s.startedAt).Seconds())
		if uptimeSeconds < 0 {
			uptimeSeconds = 0
		}
	}

	return runtimeMetricsPayload{
		Timestamp:     now.UnixMilli(),
		Version:       s.version,
		UptimeSeconds: uptimeSeconds,
		CPUPercent:    cpuPercent,
		Memory: runtimeMemoryUsage{
			Total:       vm.Total,
			Used:        vm.Used,
			Free:        vm.Free,
			UsedPercent: vm.UsedPercent,
		},
		Disk: runtimeDiskUsage{
			Path:        cwd,
			Total:       du.Total,
			Used:        du.Used,
			Free:        du.Free,
			UsedPercent: du.UsedPercent,
		},
		Network: s.sampleRuntimeNetwork(now),
		GPU:     s.sampleRuntimeGPU(),
	}
}

func (s *Server) sampleRuntimeNetwork(now time.Time) runtimeNetworkUsage {
	counters, err := gopsnet.IOCounters(false)
	if err != nil || len(counters) == 0 {
		return runtimeNetworkUsage{}
	}

	current := counters[0]
	s.runtimeMetricsMu.Lock()
	defer s.runtimeMetricsMu.Unlock()

	if s.runtimeNetLastSampleAt.IsZero() {
		s.runtimeNetLastSampleAt = now
		s.runtimeNetLastRXBytes = current.BytesRecv
		s.runtimeNetLastTXBytes = current.BytesSent
		return runtimeNetworkUsage{
			RXBPS:   0,
			TXBPS:   0,
			RXBytes: current.BytesRecv,
			TXBytes: current.BytesSent,
		}
	}

	elapsedSeconds := now.Sub(s.runtimeNetLastSampleAt).Seconds()
	if elapsedSeconds > 0 {
		rxDelta := int64(current.BytesRecv) - int64(s.runtimeNetLastRXBytes)
		txDelta := int64(current.BytesSent) - int64(s.runtimeNetLastTXBytes)
		if rxDelta < 0 {
			rxDelta = 0
		}
		if txDelta < 0 {
			txDelta = 0
		}
		s.runtimeNetCurrentRXBPS = float64(rxDelta) / elapsedSeconds
		s.runtimeNetCurrentTXBPS = float64(txDelta) / elapsedSeconds
	}

	s.runtimeNetLastSampleAt = now
	s.runtimeNetLastRXBytes = current.BytesRecv
	s.runtimeNetLastTXBytes = current.BytesSent
	return runtimeNetworkUsage{
		RXBPS:   s.runtimeNetCurrentRXBPS,
		TXBPS:   s.runtimeNetCurrentTXBPS,
		RXBytes: current.BytesRecv,
		TXBytes: current.BytesSent,
	}
}

func (s *Server) sampleRuntimeGPU() runtimeGPUUsage {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return runtimeGPUUsage{
			Supported: false,
			Message:   "当前平台未启用GPU采集",
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx,
		"nvidia-smi",
		"--query-gpu=utilization.gpu,memory.used,memory.total",
		"--format=csv,noheader,nounits",
	)
	out, err := cmd.Output()
	if err != nil {
		return runtimeGPUUsage{
			Supported: false,
			Message:   "GPU采集失败",
		}
	}

	line := strings.TrimSpace(string(out))
	if line == "" {
		return runtimeGPUUsage{
			Supported: false,
			Message:   "GPU输出为空",
		}
	}
	firstLine := strings.TrimSpace(strings.Split(line, "\n")[0])
	parts := strings.Split(firstLine, ",")
	if len(parts) < 3 {
		return runtimeGPUUsage{
			Supported: false,
			Message:   "GPU输出格式异常",
		}
	}
	utilPercent, _ := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	memUsedMB, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	memTotalMB, _ := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
	return runtimeGPUUsage{
		Supported:     true,
		UtilPercent:   utilPercent,
		MemoryUsedMB:  memUsedMB,
		MemoryTotalMB: memTotalMB,
		Message:       "ok",
	}
}
