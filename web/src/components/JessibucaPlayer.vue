<script setup lang="ts">
import Hls from 'hls.js'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { playbackAPI } from '@/api/modules'

const props = withDefaults(defineProps<{
  url: string
  hasAudio?: boolean
  streamApp?: string
  streamId?: string
}>(), {
  hasAudio: false,
  streamApp: '',
  streamId: '',
})

type PlayMode = 'none' | 'flv' | 'hls' | 'webrtc'
type PlayerState = 'idle' | 'connecting' | 'playing' | 'error'

const jessibucaContainer = ref<HTMLDivElement | null>(null)
const videoElement = ref<HTMLVideoElement | null>(null)
const status = ref<PlayerState>('idle')
const errorMsg = ref('')

type JessibucaCtor = NonNullable<typeof window.Jessibuca>
type JessibucaInstance = InstanceType<JessibucaCtor>

let jessibuca: JessibucaInstance | null = null
let hls: Hls | null = null
let rtcPC: RTCPeerConnection | null = null
let runSeq = 0
let reconnectTimer: number | null = null
let reconnectInFlight = false
let reconnectTaskID = 0
const reconnectDelayMS = 2000

const rawURL = computed(() => String(props.url || '').trim())
const streamApp = computed(() => String(props.streamApp || '').trim())
const streamID = computed(() => String(props.streamId || '').trim())
const canReconnect = computed(() => rawURL.value.length > 0 && streamID.value.length > 0)

function isWebRTCSignalURL(url: string): boolean {
  try {
    const parsed = new URL(url)
    return parsed.pathname.includes('/index/api/webrtc')
  } catch {
    return false
  }
}

function detectMode(url: string): PlayMode {
  const v = String(url || '').trim()
  if (!v) return 'none'
  if (/^webrtc:\/\//i.test(v) || isWebRTCSignalURL(v)) return 'webrtc'
  if (/\.m3u8(\?|$)/i.test(v)) return 'hls'
  return 'flv'
}

const mode = computed(() => detectMode(rawURL.value))
const showVideo = computed(() => mode.value === 'hls' || mode.value === 'webrtc')
const statusText = computed(() => {
  if (status.value === 'idle') return '空闲'
  if (status.value === 'connecting') return '连接中'
  if (status.value === 'playing') return '播放中'
  if (status.value === 'error') return '异常'
  return status.value
})

function isPromiseLike(value: unknown): value is Promise<unknown> {
  return Boolean(value) && typeof (value as { then?: unknown }).then === 'function'
}

function setError(message: string) {
  status.value = 'error'
  errorMsg.value = message
}

function clearReconnectTimer() {
  if (reconnectTimer !== null) {
    window.clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
}

function cancelReconnect() {
  reconnectTaskID += 1
  reconnectInFlight = false
  clearReconnectTimer()
}

async function queryStreamActive(app: string, stream: string): Promise<boolean> {
  const payload = await playbackAPI.streamStatus(app, stream) as { active?: boolean }
  return Boolean(payload?.active)
}

async function queueReconnect(message: string, seq: number) {
  if (seq !== runSeq) return
  setError(message)
  if (!canReconnect.value || reconnectInFlight || reconnectTimer !== null) return

  const app = streamApp.value
  const stream = streamID.value
  if (!stream) return

  const taskID = reconnectTaskID + 1
  reconnectTaskID = taskID
  reconnectInFlight = true

  await stopPlayback()
  if (seq !== runSeq || taskID != reconnectTaskID) {
    reconnectInFlight = false
    return
  }
  setError(message)

  let active = false
  try {
    active = await queryStreamActive(app, stream)
  } catch {
    if (seq !== runSeq || taskID != reconnectTaskID) {
      reconnectInFlight = false
      return
    }
    reconnectInFlight = false
    reconnectTimer = window.setTimeout(() => {
      if (seq !== runSeq || taskID != reconnectTaskID) return
      reconnectTimer = null
      void queueReconnect(message, seq)
    }, reconnectDelayMS)
    return
  }

  if (seq !== runSeq || taskID != reconnectTaskID) {
    reconnectInFlight = false
    return
  }

  reconnectInFlight = false
  if (!active) {
    setError(message)
    return
  }

  reconnectTimer = window.setTimeout(() => {
    if (seq !== runSeq || taskID != reconnectTaskID) return
    reconnectTimer = null
    void startPlayback()
  }, reconnectDelayMS)
}

function resetVideoElement() {
  const video = videoElement.value
  if (!video) return
  video.onplaying = null
  video.onpause = null
  video.onwaiting = null
  video.onerror = null
  try {
    video.pause()
  } catch {
    // ignore pause errors
  }
  if (video.srcObject) {
    const stream = video.srcObject as MediaStream
    stream.getTracks().forEach((track) => track.stop())
    video.srcObject = null
  }
  if (video.getAttribute('src')) {
    video.removeAttribute('src')
    video.load()
  }
}

async function cleanupJessibuca() {
  if (!jessibuca) return
  const player = jessibuca
  jessibuca = null
  try {
    const maybePromise = player.destroy()
    if (isPromiseLike(maybePromise)) {
      await maybePromise
    }
  } catch {
    // ignore destroy errors from internal async race
  }
  if (jessibucaContainer.value) {
    jessibucaContainer.value.innerHTML = ''
  }
}

function cleanupHLS() {
  if (!hls) return
  try {
    hls.destroy()
  } catch {
    // ignore hls destroy errors
  }
  hls = null
}

function cleanupWebRTC() {
  if (rtcPC) {
    try {
      rtcPC.ontrack = null
      rtcPC.onconnectionstatechange = null
      rtcPC.oniceconnectionstatechange = null
      rtcPC.close()
    } catch {
      // ignore rtc close errors
    }
    rtcPC = null
  }
}

async function stopPlayback() {
  cleanupWebRTC()
  cleanupHLS()
  await cleanupJessibuca()
  resetVideoElement()
  status.value = 'idle'
}

function bindVideoLifecycle(seq: number) {
  const video = videoElement.value
  if (!video) return
  video.onplaying = () => {
    if (seq !== runSeq) return
    status.value = 'playing'
    errorMsg.value = ''
  }
  video.onwaiting = () => {
    if (seq !== runSeq) return
    status.value = 'connecting'
  }
  video.onerror = () => {
    if (seq !== runSeq) return
    setError('视频播放失败')
  }
}

function bindRetryableVideoLifecycle(seq: number) {
  bindVideoLifecycle(seq)
  const video = videoElement.value
  if (!video) return
  video.onerror = () => {
    if (seq !== runSeq) return
    void queueReconnect('视频播放失败', seq)
  }
}

async function startFLV(url: string, seq: number) {
  await nextTick()
  if (seq !== runSeq) return

  if (!window.Jessibuca) {
    setError('Jessibuca 脚本未加载')
    return
  }
  if (!jessibucaContainer.value) {
    setError('播放器容器未就绪')
    return
  }

  status.value = 'connecting'
  errorMsg.value = ''

  try {
    const player = new window.Jessibuca({
      container: jessibucaContainer.value,
      decoder: '/assets/js/decoder.js',
      videoBuffer: 0.2,
      forceNoOffscreen: true,
      isResize: true,
      hasAudio: props.hasAudio,
      loadingText: '加载中...',
      useMSE: true,
      useWCS: true,
      autoWasm: true,
      supportDblclickFullscreen: true,
      showBandwidth: false,
    })
    jessibuca = player

    player.on('play', () => {
      if (seq !== runSeq) return
      status.value = 'playing'
      errorMsg.value = ''
    })
    player.on('error', (...args: unknown[]) => {
      if (seq !== runSeq) return
      void queueReconnect(`播放器异常: ${args.map((v) => String(v)).join(' ')}`, seq)
    })
    player.on('error', (...args: unknown[]) => {
      if (seq !== runSeq) return
      setError(`播放器异常: ${args.map((v) => String(v)).join(' ')}`)
    })

    const maybePromise = player.play(url)
    if (isPromiseLike(maybePromise)) {
      maybePromise.catch((err: unknown) => {
        if (seq !== runSeq) return
        void queueReconnect((err as Error)?.message || '播放失败', seq)
      })
      maybePromise.catch((err: unknown) => {
        if (seq !== runSeq) return
        setError((err as Error)?.message || '播放失败')
      })
    }
  } catch (err) {
    if (seq !== runSeq) return
    setError((err as Error).message || '初始化播放器失败')
  }
}

async function startHLS(url: string, seq: number) {
  await nextTick()
  if (seq !== runSeq) return

  const video = videoElement.value
  if (!video) {
    setError('视频元素未就绪')
    return
  }

  status.value = 'connecting'
  errorMsg.value = ''
  video.muted = !props.hasAudio
  video.autoplay = true
  video.playsInline = true
  video.controls = false
  bindVideoLifecycle(seq)

  if (Hls.isSupported()) {
    const instance = new Hls({
      enableWorker: true,
      lowLatencyMode: true,
      backBufferLength: 30,
      maxBufferLength: 12,
      maxMaxBufferLength: 20,
      liveDurationInfinity: true,
    })
    hls = instance

    instance.on(Hls.Events.MANIFEST_PARSED, () => {
      if (seq !== runSeq) return
      void video.play().catch(() => {
        // autoplay may be blocked on some browsers
      })
    })
    instance.on(Hls.Events.ERROR, (_, data) => {
      if (seq !== runSeq) return
      if (!data.fatal) return
      if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
        instance.startLoad()
        return
      }
      if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
        instance.recoverMediaError()
        return
      }
      setError(`HLS 播放失败: ${data.type} ${data.details}`)
    })
    instance.loadSource(url)
    instance.attachMedia(video)
    return
  }

  if (video.canPlayType('application/vnd.apple.mpegurl')) {
    video.src = url
    try {
      await video.play()
    } catch {
      // autoplay may be blocked on some browsers
    }
    return
  }

  setError('当前浏览器不支持 HLS 播放')
}

async function waitICEGatheringComplete(pc: RTCPeerConnection, timeoutMS = 1500) {
  if (pc.iceGatheringState === 'complete') return
  await new Promise<void>((resolve) => {
    const timer = window.setTimeout(done, timeoutMS)
    function done() {
      clearTimeout(timer)
      pc.removeEventListener('icegatheringstatechange', onStateChange)
      resolve()
    }
    function onStateChange() {
      if (pc.iceGatheringState === 'complete') {
        done()
      }
    }
    pc.addEventListener('icegatheringstatechange', onStateChange)
  })
}

function buildWebRTCAPIURL(url: string): string {
  const preferred = window.location.protocol === 'https:' ? 'https://' : 'http://'
  const httpURL = url.replace(/^webrtc:\/\//i, preferred)
  const parsed = new URL(httpURL)

  if (parsed.pathname.includes('/index/api/webrtc')) {
    if (!parsed.searchParams.get('type')) {
      parsed.searchParams.set('type', 'play')
    }
    return parsed.toString()
  }

  const segments = parsed.pathname.split('/').filter(Boolean)
  if (segments.length < 2) {
    throw new Error('webrtc 地址格式错误，无法解析 app/stream')
  }
  const stream = segments.pop() as string
  const app = segments.pop() as string
  parsed.pathname = '/index/api/webrtc'
  parsed.search = ''
  parsed.searchParams.set('app', app)
  parsed.searchParams.set('stream', stream)
  parsed.searchParams.set('type', 'play')
  return parsed.toString()
}

function extractAnswerSDP(payload: unknown, rawText: string): string {
  if (typeof payload === 'string' && payload.includes('v=0')) {
    return payload
  }
  if (payload && typeof payload === 'object') {
    const obj = payload as Record<string, unknown>
    if ('code' in obj && Number(obj.code) !== 0) {
      throw new Error(String(obj.msg || `webrtc 信令失败 code=${String(obj.code)}`))
    }
    if (typeof obj.sdp === 'string' && obj.sdp.trim()) {
      return obj.sdp
    }
    const data = obj.data as Record<string, unknown> | undefined
    if (data && typeof data.sdp === 'string' && data.sdp.trim()) {
      return data.sdp
    }
  }
  if (rawText.includes('v=0')) {
    return rawText
  }
  throw new Error('webrtc 应答 SDP 为空')
}

async function startWebRTC(url: string, seq: number) {
  await nextTick()
  if (seq !== runSeq) return

  const video = videoElement.value
  if (!video) {
    setError('视频元素未就绪')
    return
  }

  status.value = 'connecting'
  errorMsg.value = ''
  video.muted = !props.hasAudio
  video.autoplay = true
  video.playsInline = true
  video.controls = false
  bindRetryableVideoLifecycle(seq)

  const signalURL = buildWebRTCAPIURL(url)
  const pc = new RTCPeerConnection()
  rtcPC = pc

  const remoteStream = new MediaStream()
  video.srcObject = remoteStream

  pc.ontrack = (event) => {
    if (seq !== runSeq) return
    for (const track of event.streams[0]?.getTracks() || []) {
      remoteStream.addTrack(track)
    }
    if (event.streams.length === 0 && event.track) {
      remoteStream.addTrack(event.track)
    }
    void video.play().catch(() => {
      // autoplay may be blocked on some browsers
    })
  }

  pc.onconnectionstatechange = () => {
    if (seq !== runSeq) return
    if (pc.connectionState === 'connected') {
      status.value = 'connecting'
      errorMsg.value = ''
    }
    if (pc.connectionState === 'failed' || pc.connectionState === 'closed') {
      void queueReconnect('WebRTC 连接失败', seq)
    }
    if (pc.connectionState === 'failed') {
      setError('WebRTC 连接失败')
    }
  }

  pc.oniceconnectionstatechange = () => {
    if (seq !== runSeq) return
    if (pc.iceConnectionState === 'disconnected') {
      status.value = 'connecting'
    }
    if (pc.iceConnectionState === 'failed' || pc.iceConnectionState === 'closed') {
      void queueReconnect('WebRTC ICE 连接失败', seq)
    }
    if (pc.iceConnectionState === 'failed') {
      setError('WebRTC ICE 连接失败')
    }
  }

  try {
    pc.addTransceiver('video', { direction: 'recvonly' })
    pc.addTransceiver('audio', { direction: 'recvonly' })

    const offer = await pc.createOffer()
    await pc.setLocalDescription(offer)
    await waitICEGatheringComplete(pc, 1200)
    if (seq !== runSeq) return

    const offerSDP = pc.localDescription?.sdp
    if (!offerSDP) {
      throw new Error('生成 WebRTC offer 失败')
    }

    const response = await fetch(signalURL, {
      method: 'POST',
      headers: {
        'Content-Type': 'text/plain',
      },
      body: offerSDP,
    })
    const rawText = await response.text()
    if (!response.ok) {
      throw new Error(`webrtc 信令请求失败(${response.status}): ${rawText.slice(0, 120)}`)
    }

    let payload: unknown = rawText
    try {
      payload = JSON.parse(rawText)
    } catch {
      payload = rawText
    }

    const answerSDP = extractAnswerSDP(payload, rawText)
    if (seq !== runSeq) return

    await pc.setRemoteDescription(new RTCSessionDescription({ type: 'answer', sdp: answerSDP }))
  } catch (err) {
    if (seq !== runSeq) return
    void queueReconnect((err as Error).message || 'WebRTC 播放失败', seq)
    setError((err as Error).message || 'WebRTC 播放失败')
  }
}

async function startPlayback() {
  cancelReconnect()
  const seq = ++runSeq
  await stopPlayback()
  if (seq !== runSeq) return

  const url = rawURL.value
  if (!url) return

  const playMode = detectMode(url)
  if (playMode === 'flv') {
    await startFLV(url, seq)
    return
  }
  if (playMode === 'hls') {
    await startHLS(url, seq)
    return
  }
  if (playMode === 'webrtc') {
    await startWebRTC(url, seq)
  }
}

watch(() => [props.url, props.hasAudio, props.streamApp, props.streamId], () => { void startPlayback() }, { flush: 'post' })

onMounted(() => { void startPlayback() })
onBeforeUnmount(() => {
  cancelReconnect()
  runSeq += 1
  void stopPlayback()
})
</script>

<template>
  <div class="player-wrap">
    <div v-show="mode === 'flv'" ref="jessibucaContainer" class="player-canvas" />
    <video v-show="showVideo" ref="videoElement" class="player-video" />
    <div v-if="!rawURL" class="placeholder">暂无播放地址</div>
    <div v-if="errorMsg" class="overlay">{{ errorMsg }}</div>
    <div class="status">{{ statusText }}</div>
  </div>
</template>

<style scoped>
.player-wrap {
  position: relative;
  width: 100%;
  height: 100%;
  min-height: 120px;
  border-radius: 12px;
  overflow: hidden;
  background: linear-gradient(135deg, #122719, #1f3b2a);
}

.player-canvas {
  width: 100%;
  height: 100%;
}

.player-video {
  width: 100%;
  height: 100%;
  background: #000;
  object-fit: contain;
}

.placeholder {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: rgba(226, 247, 215, 0.72);
  font-size: 12px;
  letter-spacing: 0.08em;
}

.overlay {
  position: absolute;
  inset: auto 8px 8px 8px;
  background: rgba(0, 0, 0, 0.5);
  border-radius: 8px;
  color: #ffd2d2;
  padding: 6px 8px;
  font-size: 12px;
}

.status {
  position: absolute;
  left: 8px;
  top: 8px;
  background: rgba(0, 0, 0, 0.4);
  color: #f2ffea;
  border-radius: 999px;
  padding: 2px 8px;
  font-size: 11px;
  text-transform: uppercase;
}
</style>
