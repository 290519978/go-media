<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { Upload, message } from 'ant-design-vue'
import { algorithmAPI, yoloLabelAPI, type AlgorithmImportResult, type AlgorithmUpsertPayload } from '@/api/modules'
import { appendTokenQuery } from '@/api/request'
import { useAuthStore } from '@/stores/auth'
import { formatDateTime } from '@/utils/datetime'

type Algorithm = {
  id: string
  code: string
  name: string
  description: string
  image_url: string
  scene: string
  category: string
  mode: 'small' | 'large' | 'hybrid'
  enabled: boolean
  small_model_label: string | string[]
  detect_mode: 1 | 2 | 3
  yolo_threshold: number
  iou_threshold: number
  labels_trigger_mode: 'any' | 'all'
  active_prompt?: { id: string; version: string; prompt: string }
}

type Label = {
  label: string
  name: string
}

type TestRecord = {
  id: string
  algorithm_id: string
  batch_id: string
  media_type: 'image' | 'video'
  media_path: string
  original_file_name: string
  image_path: string
  request_payload: string
  response_payload: string
  success: boolean
  basis?: string
  conclusion?: string
  normalized_boxes?: NormalizedBox[]
  anomaly_times?: TestAnomalyTime[]
  duration_seconds?: number
  file_name?: string
  media_url?: string
  created_at: string
}

type NormalizedBox = {
  label: string
  confidence: number
  x: number
  y: number
  w: number
  h: number
}

type TestAnomalyTime = {
  timestamp_ms: number
  timestamp_text: string
  reason: string
}

type TestResultItem = {
  job_item_id?: string
  sort_order?: number
  status?: 'pending' | 'running' | 'success' | 'failed'
  record_id: string
  file_name: string
  media_type: 'image' | 'video'
  success: boolean
  conclusion: string
  basis: string
  media_url: string
  normalized_boxes?: NormalizedBox[]
  anomaly_times?: TestAnomalyTime[]
  duration_seconds?: number
  preview_url?: string
  client_key?: string
  error_message?: string
}

type TestJobStatus = 'pending' | 'running' | 'completed' | 'partial_failed' | 'failed'

type TestJobSnapshot = {
  job_id: string
  batch_id: string
  algorithm_id: string
  status: TestJobStatus
  total_count: number
  success_count: number
  failed_count: number
  items: TestResultItem[]
}

const loading = ref(false)
const algorithms = ref<Algorithm[]>([])
const labels = ref<Label[]>([])

const algorithmModal = ref(false)
const editingAlgorithmID = ref('')
const algorithmForm = reactive({
  code: '',
  name: '',
  description: '',
  image_url: '',
  scene: '',
  category: '',
  enabled: true,
  small_model_label: [] as string[],
  detect_mode: 3 as 1 | 2 | 3,
  yolo_threshold: 0.5,
  iou_threshold: 0.8,
  labels_trigger_mode: 'any' as 'any' | 'all',
})

const promptModal = ref(false)
const promptAlgorithmID = ref('')
const prompts = ref<any[]>([])
const promptForm = reactive({ version: 'v1', prompt: '' })

const testModal = ref(false)
const testingAlgorithm = ref<Algorithm | null>(null)
const testingFiles = ref<any[]>([])
const testingUploadList = ref<any[]>([])
const testResults = ref<TestResultItem[]>([])
const testResultPreviewURLs = ref<string[]>([])
const uploadingCover = ref(false)
const runningTest = ref(false)
const currentTestJobID = ref('')
const currentTestJobStatus = ref<TestJobStatus>('pending')
const currentTestAlgorithmID = ref('')
let testJobPollTimer: number | null = null
const importingAlgorithms = ref(false)
const importUploadUID = ref('')

const testHistoryModal = ref(false)
const historyAlgorithm = ref<Algorithm | null>(null)
const historyLoading = ref(false)
const clearTestsLoading = ref(false)
const testRecords = ref<TestRecord[]>([])
const testBoxModal = ref(false)
const testBoxTitle = ref('')
const testBoxImageURL = ref('')
const testBoxList = ref<NormalizedBox[]>([])
const mediaPreviewModal = ref(false)
const mediaPreviewTitle = ref('')
const mediaPreviewType = ref<'image' | 'video'>('image')
const mediaPreviewURL = ref('')
const testBoxAspectRatio = '16 / 9'
const testLimits = reactive({
  image_max_count: 5,
  video_max_count: 1,
  video_max_bytes: 100 * 1024 * 1024,
})
const testPager = reactive({
  page: 1,
  page_size: 10,
  total: 0,
  total_pages: 0,
})
const payloadModal = ref(false)
const payloadTitle = ref('')
const payloadText = ref('')
const authStore = useAuthStore()
const isDevelopmentMode = computed(() => authStore.developmentMode)

const labelOptions = computed(() => labels.value.map((l) => ({ label: `${l.label} (${l.name || '-'})`, value: l.label })))
const yoloLabelNameMap = computed(() => {
  const out = new Map<string, string>()
  for (const item of labels.value) {
    const key = String(item?.label || '').trim().toLowerCase()
    const name = String(item?.name || '').trim()
    if (!key || !name) continue
    out.set(key, name)
  }
  return out
})
const imageBase = import.meta.env.VITE_API_BASE_URL || ''
const detectModeOptions = [
  { label: '模式 1（仅小模型）', value: 1 },
  { label: '模式 2（仅大模型）', value: 2 },
  { label: '模式 3（小模型门控后调用大模型）', value: 3 },
]
const labelsTriggerModeOptions = [
  { label: '任意标签命中', value: 'any' },
  { label: '全部标签命中', value: 'all' },
]

async function loadAll() {
  loading.value = true
  try {
    const [a, l, limits] = await Promise.all([
      algorithmAPI.list() as Promise<{ items: any[] }>,
      yoloLabelAPI.list() as Promise<{ items: Label[] }>,
      algorithmAPI.testLimits() as Promise<{ image_max_count?: number; video_max_count?: number; video_max_bytes?: number }>,
    ])
    algorithms.value = (a.items || []).map((item: any) =>
      item.algorithm ? { ...item.algorithm, active_prompt: item.active_prompt } : item,
    )
    labels.value = l.items || []
    testLimits.image_max_count = Number(limits?.image_max_count || 5)
    testLimits.video_max_count = Number(limits?.video_max_count || 1)
    testLimits.video_max_bytes = Number(limits?.video_max_bytes || 100 * 1024 * 1024)
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    loading.value = false
  }
}

function openCreateAlgorithm() {
  editingAlgorithmID.value = ''
  Object.assign(algorithmForm, {
    code: '',
    name: '',
    description: '',
    image_url: '',
    scene: '',
    category: '',
    enabled: true,
    small_model_label: [] as string[],
    detect_mode: 3 as 1 | 2 | 3,
    yolo_threshold: 0.5,
    iou_threshold: 0.8,
    labels_trigger_mode: 'any',
  })
  algorithmModal.value = true
}

function openEditAlgorithm(row: Algorithm) {
  editingAlgorithmID.value = row.id
  Object.assign(algorithmForm, {
    code: row.code || '',
    name: row.name,
    description: row.description || '',
    image_url: row.image_url || '',
    scene: row.scene || '',
    category: row.category || '',
    enabled: row.enabled,
    small_model_label: parseSmallModelLabels(row.small_model_label),
    detect_mode: Number(row.detect_mode || 3) === 1 ? 1 : Number(row.detect_mode || 3) === 2 ? 2 : 3,
    yolo_threshold: Number(row.yolo_threshold || 0.5),
    iou_threshold: Number(row.iou_threshold || 0.8),
    labels_trigger_mode: row.labels_trigger_mode === 'all' ? 'all' : 'any',
  })
  algorithmModal.value = true
}

async function submitAlgorithm() {
  const normalizedCode = String(algorithmForm.code || '').trim().toUpperCase()
  if (!/^[A-Z][A-Z0-9_]{1,31}$/.test(normalizedCode)) {
    message.warning('缂傚倸鍊搁崐鎼佸磹閹间礁纾归柟闂寸绾惧綊鏌熼梻瀵割槮缁炬儳缍婇弻鐔兼⒒鐎靛壊妲紒鐐劤缂嶅﹪寮婚敐澶婄闁挎繂鎲涢幘缁樼厱闁靛牆鎳庨顓㈡煛鐏炶鈧鍒掑▎鎴炲磯闁靛鍊楁す鎶芥⒒娴ｅ憡鍟為柣鐔村劤閹广垹顫滈埀顒€顕ｆ繝姘櫜濠㈣泛顭濠囨⒑閺傘儲娅呴柛鐕佸亝缁傛帡鍩￠崨顔规嫼闂備緡鍋嗛崑娑㈡嚐椤栨稒娅犳い鏇楀亾闁哄本鐩幃銏ゅ川婵犲嫮鈻忛梻浣风串缁插潡宕楀Ο铏规殾闁跨喓濮甸崐鐑芥煃閸濆嫬鈧鍩€椤掆偓椤嘲顫忔ウ瑁や汗闁圭儤鍨抽崰濠囨⒑閸涘﹥灏伴柣鐔叉櫊閹即顢氶埀顒勭嵁鐎ｎ喗鏅滈悷娆欑稻鐎氳棄鈹戦悙鑸靛涧缂佽弓绮欓獮澶愭晸閻樿尙鏌堥梺缁樺姉閸庛倝鎮″▎鎾寸厱婵犻潧瀚崝姘舵煕濡粯灏﹂柡宀€鍠栭幖褰掝敃椤愶絿鏉归柣搴ゎ潐濞叉鍒掑澶婄闁告稒娼欑粈鍫ユ煕濞嗗浚妯堟俊顐犲劜缁绘繈鎮介棃娑楃捕濡炪倖娲﹂崢浠嬪箞閵娾晛绠绘い鏇炴噺閺呯偤姊虹化鏇炲⒉缂佸甯″畷鎴﹀磼濞戞氨顔曢梺鐟邦嚟閸嬫盯鎮炶ぐ鎺撶厱閻庯綆鍋呭畷宀勬煟濞戝崬娅嶇€规洖缍婇、娆撴偂鎼搭喗缍撳┑鐘茬棄閺夊簱鍋撳Δ浣瑰弿闁圭虎鍠栫粻鐔兼煥濞戞ê顏柣顓烆槺閳ь剙绠嶉崕閬嵥囨导鏉戠９闁绘垼濮ら悡鐔兼煙鐎电鍓遍柣鎺嶇矙閺屾稑顫滈埀顒佺鐠轰警娼栨繛宸簼椤ュ牊绻涢幋锝勫惈闁告瑦姘ㄧ槐鎾存媴閸濆嫅锝夋煕閵娿儲鍋ョ€规洘妞芥慨鈧柕鍫濇噹瀹撳棝姊洪棃娑㈢崪缂佽鲸娲熷畷銏ゅ箹娴ｅ湱鍘介棅顐㈡处濞叉牗绂掕閻ヮ亪顢橀悙闈涚厽閻庤娲橀崹鍧楃嵁濡吋瀚氶柤纰卞墰閺夌鈹戦悙鑸靛涧缂佽弓绮欓獮澶愭晸閻樿尙锛欏銈嗙墱閸嬬偤鍩涢幒鎳ㄥ綊鏁愰崼顐ｇ秷闂佺顑囨繛鈧柡灞剧⊕閹棃濮€閵忋垻鍘滈柣搴ゎ潐濞叉﹢鈥﹂崼銉嬪洭骞橀钘変缓濡炪倖鐗楃粙鎾诲Υ閹烘梻纾奸弶鍫涘妽瀹曞瞼鈧娲樼敮鎺楋綖濠靛鏁勯柦妯侯槷婢规洟姊虹紒妯虹伇濠殿喓鍊濆畷鎰板锤濡や胶鍙嗛梺鍝勬川閸嬫盯鍩€椤掍焦鍊愰柟铏矎閵囨劙骞掗幘璺哄箻濠电姵顔栭崰妤呭礉閺囥垹鐒垫い鎺嶈兌婢ь亪鏌ｉ敐鍥у幋鐎规洩绲惧鍕暆閳ь剟鎯侀崼銉︹拻闁稿本姘ㄦ晶娑樸€掑顓ф疁鐎规洘娲濈粻娑樷槈濞嗘垵寮梻浣告啞閸旓附绂嶅鍫濆嚑婵炴垯鍨洪悡娑㈡倶閻愰潧浜剧紒鈧崘顏嗙＜缂備焦顭囧ú瀵糕偓瑙勬礀缂嶅﹪銆佸▎鎾村亗閹兼惌鍠楃紞宥夋⒒閸屾艾鈧绮堟笟鈧獮鏍敃閿旂粯鏅為梺鍛婄☉閻°劑鎮￠埀顒勬⒑閹稿海绠撴い锔诲灦閹锋垿鎮㈤崗鑲╁幗闂佸搫鍊搁悘婵嬪箖閹达附鐓曞┑鐘插鐢稑菐閸パ嶈含妞ゃ垺绋戦埞鎴﹀幢閺囩喓娼栭梻鍌欐缁鳖喚绱炴担鍓茬劷鐟滄棃宕洪悙鍝勭闁挎棁妫勯埀顒傚厴閺屻倗鍠婇崡鐐差潻濡炪倧绲块弫璇差潖濞差亜浼犻柛鏇ㄥ墮濞呇勭節濞堝灝鏋熼柟鍛婂▕閺佹劙鎮欓崜浣烘澑闂佺懓褰為悞锕€顪冩禒瀣ㄢ偓渚€寮崼婵堫槹濡炪倖鎸嗛崘鈺傛瘑闂傚倸鍊烽懗鍫曘€佹繝鍐彾闁割偁鍎辩粻鍨亜韫囨挻顥滅紒韬插€濋弻娑樷攽閸℃寮搁梺鎸庣箓椤︻垶鏌嬮崶顒佺厪濠㈣埖绋栭崥鍌炴煙瀹勭増鎯堥柍瑙勫灴閹瑧鎹勬ウ鎸庢毐婵＄偑鍊栧▔锕傚炊閳轰胶銈﹂梻浣告惈缁嬩線宕㈡禒瀣亗闁哄洨鍋愰弨浠嬫煟濡绲婚柡鍡欏仱閺屾稒绻濋崘銊ヮ潓闂佸疇顫夐崹鍧楀春閸曨垰绀冮柍杞扮閺€顓熶繆閵堝洤啸闁稿鍋熼弫顕€鍨鹃幇浣告濡炪倖娲嶉崑鎾绘煕閳规儳浜炬俊鐐€栧濠氬磻閹剧粯鐓熼柨婵嗩槹閺佽京鈧灚婢樼€氼厾鎹㈠☉娆愬劅闁规儳鍘栨竟鏇㈡⒑閸︻厼鍔嬮柛鈺佺墕椤洭鍩￠崨顔惧幗闂佽宕樺▍鏇㈠箲閿濆悿褰掓偑閳ь剟宕圭捄渚綎婵炲樊浜滃婵嗏攽閻樻彃顏柛锛卞啠鏀介柍钘夋娴滄繄绱掔拠鎻掆偓鍧楁晲閻愬墎鐤€闁哄啫鍊婚惁鍫濃攽椤旀枻渚涢柛鎾寸洴钘濋柡澶嬵儥濞撳鏌曢崼婵囶棞濠殿啫鍛＜闂婎偒鍘鹃惌娆戔偓娈垮枟閹倿骞冮埡鍐＜婵☆垵銆€閸?^[A-Z][A-Z0-9_]{1,31}$')
    return
  }
  algorithmForm.code = normalizedCode
  algorithmForm.yolo_threshold = clampNumber(algorithmForm.yolo_threshold, 0.01, 0.99, 0.5)
  algorithmForm.iou_threshold = clampNumber(algorithmForm.iou_threshold, 0.1, 0.99, 0.8)
  algorithmForm.detect_mode = algorithmForm.detect_mode === 1 ? 1 : algorithmForm.detect_mode === 2 ? 2 : 3
  if (
    algorithmForm.detect_mode !== 2 &&
    (!Array.isArray(algorithmForm.small_model_label) || algorithmForm.small_model_label.length === 0)
  ) {
    message.warning('When detect_mode is 1 or 3, labels are required')
    return
  }
  if (algorithmForm.detect_mode === 2) {
    algorithmForm.small_model_label = []
  }
  algorithmForm.labels_trigger_mode = algorithmForm.labels_trigger_mode === 'all' ? 'all' : 'any'
  const payload = {
    ...algorithmForm,
    mode: 'hybrid',
  }
  try {
    if (editingAlgorithmID.value) {
      await algorithmAPI.update(editingAlgorithmID.value, payload)
      message.success('Algorithm updated')
    } else {
      await algorithmAPI.create(payload)
      message.success('Algorithm created')
    }
    algorithmModal.value = false
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
  }
}

function onImportUploadRequest(options: any) {
  if (typeof options?.onSuccess === 'function') {
    options.onSuccess({}, options.file)
  }
}

function normalizeImportPayload(raw: unknown): AlgorithmUpsertPayload[] {
  if (Array.isArray(raw)) {
    return raw as AlgorithmUpsertPayload[]
  }
  if (raw && typeof raw === 'object' && Array.isArray((raw as { items?: unknown[] }).items)) {
    return ((raw as { items?: unknown[] }).items || []) as AlgorithmUpsertPayload[]
  }
  return []
}

async function importAlgorithmsFromFile(file: File) {
  importingAlgorithms.value = true
  try {
    const content = await file.text()
    const text = String(content || '').trim()
    if (!text) {
      message.warning('导入文件为空')
      return
    }
    const parsed = JSON.parse(text)
    const payload = normalizeImportPayload(parsed)
    if (payload.length === 0) {
      message.warning('导入数据格式错误，需为 JSON 数组')
      return
    }
    const result = await algorithmAPI.import(payload) as AlgorithmImportResult
    const summary = `导入完成：总数 ${result.total}，新增 ${result.created}，更新 ${result.updated}，失败 ${result.failed}`
    if (result.failed > 0) {
      const details = (result.errors || [])
        .slice(0, 3)
        .map((item) => `第${item.index}条(${item.code || '-'}) ${item.message}`)
        .join('；')
      message.warning(details ? `${summary}。${details}` : summary)
    } else {
      message.success(summary)
    }
    await loadAll()
  } catch (err) {
    message.error((err as Error).message || '导入失败')
  } finally {
    importingAlgorithms.value = false
  }
}

function onImportUploadChange(info: any) {
  const file = info?.file
  if (!file) return
  const uid = String(file?.uid || '')
  if (uid && uid === importUploadUID.value) return
  if (uid) importUploadUID.value = uid
  const raw = file?.originFileObj || file
  if (!(raw instanceof File)) {
    message.error('读取导入文件失败')
    return
  }
  void importAlgorithmsFromFile(raw)
}

async function removeAlgorithm(id: string) {
  try {
    await algorithmAPI.remove(id)
    message.success('Algorithm removed')
    await loadAll()
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function openPromptManager(row: Algorithm) {
  promptAlgorithmID.value = row.id
  promptModal.value = true
  promptForm.version = 'v1'
  promptForm.prompt = ''
  try {
    const data = await algorithmAPI.listPrompts(row.id) as { items: any[] }
    prompts.value = data.items || []
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function addPrompt() {
  const version = String(promptForm.version || '').trim()
  const prompt = String(promptForm.prompt || '').trim()
  if (!version || !prompt) {
    message.warning('Version and prompt are required')
    return
  }
  promptForm.version = version
  promptForm.prompt = prompt
  try {
    await algorithmAPI.createPrompt(promptAlgorithmID.value, {
      version,
      prompt,
      is_active: false,
    })
    const data = await algorithmAPI.listPrompts(promptAlgorithmID.value) as { items: any[] }
    prompts.value = data.items || []
    promptForm.prompt = ''
    message.success('Prompt version added')
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function activatePrompt(promptId: string) {
  try {
    await algorithmAPI.activatePrompt(promptAlgorithmID.value, promptId)
    const data = await algorithmAPI.listPrompts(promptAlgorithmID.value) as { items: any[] }
    prompts.value = data.items || []
    await loadAll()
    message.success('Prompt activated')
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function removePrompt(promptId: string) {
  try {
    await algorithmAPI.deletePrompt(promptAlgorithmID.value, promptId)
    const data = await algorithmAPI.listPrompts(promptAlgorithmID.value) as { items: any[] }
    prompts.value = data.items || []
    message.success('Prompt version removed')
  } catch (err) {
    message.error((err as Error).message)
  }
}

async function onCoverUpload(file: File) {
  if (!String(file.type || '').startsWith('image/')) {
    message.error('Only image files are supported')
    return false
  }
  if (file.size > 5 * 1024 * 1024) {
    message.error('闂傚倸鍊搁崐鎼佸磹閹间礁纾归柟闂寸绾惧綊鏌熼梻瀵割槮缁炬儳缍婇弻锝夊箣閿濆憛鎾绘煕婵犲倹鍋ラ柡灞诲姂瀵挳鎮欏ù瀣壕闁告縿鍎虫稉宥夋煛瀹ュ骸骞楅柣鎾存礃閵囧嫰骞囬崜浣荷戠紓浣插亾闁逞屽墰缁辨帡鎮欓鈧崝銈嗙箾鐏忔牑鍋撻幇浣告闂佸壊鍋呭ú鏍煁閸ャ劎绠鹃柟瀵镐紳椤忓牃鈧牗寰勬繛鐐杸闂佺粯鍔曞鍫曞闯閽樺鏀介柣鎰ㄦ櫅娴滈箖姊绘担鍛婃儓閻炴凹鍋婂畷鎰攽鐎ｎ剙绁︽繛鎾村焹閸嬫挻顨ラ悙鏉戞诞妤犵偛顑呴埞鎴﹀幢閳哄倹绗庡┑鐘垫暩婵兘寮幖浣哥；婵炴垯鍨圭粻鐘绘煙闁箑骞樼紒鐘荤畺閺屽秷顧侀柛鎾跺枛瀵鏁嶉崟顏呭媰缂備緡鍨卞ú鏍ㄧ妤ｅ啯鐓欏Λ棰佽兌椤︼箓鏌熼钘夌伌婵﹥妞藉畷銊︾節閸屾凹娼庨梻浣告啞閺屻劑鎯岄崒鐐茬伋闁哄稁鍘奸柋鍥煏韫囧鐏柨娑欑箖缁绘稒娼忛崜褎鍋у銈庡幖閻楁捇濡撮崒鐐茬闁兼亽鍎抽崢閬嶆煟鎼搭垳绉甸柛鎾寸懇瀵鈽夊▎妯活啍闂佺粯鍔曞Ο濠偽ｉ搹鍦＜妞ゆ梻鏅幊鍐煃鐠囨煡鍙勬鐐叉椤︽娊鏌涙繝搴＄仩闁宠鍨块幃鈺咁敊閼测晙绱樻繝鐢靛仜椤︿即鎯勯鐐偓渚€寮介鐐茬獩闂佸搫顦伴崹褰捤囬鐑嗘富闁靛牆妫欑亸鐢告煕鎼淬垹濮囬柕鍡樺笚缁绘繂顫濋鐘插妇闂備礁澹婇崑鍛崲閸愵啟澶婎煥閸涱垳锛滃┑掳鍊愰崑鎾绘煕婵犲倹鍋ョ€殿喖顭锋俊鑸靛緞婵犲嫷妲伴梻浣藉亹閳峰牓宕滃▎鎾村亗闁绘柨鍚嬮埛鎴犵磼椤栨稒绀冮柡澶嬫そ閺屾盯濡搁妸锔绘濡炪倖娲╃紞渚€銆佸璺虹劦妞ゆ巻鍋撻柣锝囧厴楠炴帡骞婇搹顐ｎ棃闁糕斁鍋撳銈嗗笂閼冲爼銆呴弻銉︾厽闁逛即娼ф晶缁樼箾閸粎鐭欓柡宀嬬秮楠炲洭顢涘杈嚄濠电偛顕慨浼村垂娴犲钃熸繛鎴欏灩缁秹鏌嶈閸撴瑩鎮惧┑瀣濞达絾鐡曢幗鏇㈡⒑閹稿海绠撻柟鍙夛耿瀵噣宕煎┑鍫濆Е婵＄偑鍊栧濠氬磻閹剧粯鎳氶柣鎰嚟缁♀偓闂傚倸鐗婄粙鎺椝夐悩缁樼厸闁糕槅鍘鹃悾鐢告煛鐏炵偓绀夌紒鐘崇〒閳ь剨缍嗘禍鏍磻閹捐鍗抽柣妯哄悁缁楀绱撻崒娆戝妽閼垦兠瑰鍕煉闁哄矉绻濆畷姗€濡搁敃鈧ˇ鈺侇渻閵堝啫鍔氭い锔诲灦濠€渚€姊虹紒姗嗙劸妞ゆ柨锕獮澶愭倷椤掍礁寮挎繝鐢靛Т閸燁垶濡靛┑鍫氬亾鐟欏嫭绀堥柛蹇旓耿閵嗕礁顫滈埀顒勫箖濞嗘挸绠甸柟鐑樼箘閳ь剟浜跺濠氬磼濞嗘垵濡介梺璇″枛閻栫厧鐣峰┑鍡欐殕闁告洦鍓欐禍顖炴⒑缂佹ɑ灏繛瀵稿厴瀵娊鏁冮崒娑氬幗闂侀潧绻堥崺鍕倿妤ｅ啯鐓熼柟鐑樺灩娴犳盯鏌曢崶褍顏鐐村浮楠炲鈹戦崘銊ゅ闂佺厧顫曢崐鏇⑺?5MB')
    return false
  }
  uploadingCover.value = true
  try {
    const data = await algorithmAPI.uploadCover(file) as { url?: string }
    algorithmForm.image_url = String(data.url || '')
    message.success('Cover uploaded')
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    uploadingCover.value = false
  }
  return false
}

function openTestModal(row: Algorithm) {
  const sameAlgorithm = row.id === currentTestAlgorithmID.value
  const hasRunningJob = !!currentTestJobID.value && !isTestJobTerminal(currentTestJobStatus.value)
  testingAlgorithm.value = row
  if (!sameAlgorithm || !hasRunningJob) {
    resetTestModalState()
    currentTestAlgorithmID.value = row.id
  }
  testModal.value = true
  if (sameAlgorithm && hasRunningJob && currentTestJobID.value) {
    void startPollingTestJob(currentTestJobID.value)
  }
}

function onTestFileBeforeUpload(file: File) {
  const type = String(file.type || '')
  if (!type.startsWith('image/') && !type.startsWith('video/')) {
    message.error('仅支持图片和视频文件')
    return false
  }
  const nextFiles = [...testingFiles.value, file]
  const imageCount = nextFiles.filter((item) => String(item.type || '').startsWith('image/')).length
  const videoCount = nextFiles.filter((item) => String(item.type || '').startsWith('video/')).length
  if (String(file.type || '').startsWith('video/') && file.size > testLimits.video_max_bytes) {
    message.error(`视频大小不能超过 ${formatFileSize(testLimits.video_max_bytes)}`)
    return Upload.LIST_IGNORE
  }
  if (imageCount > testLimits.image_max_count) {
    message.error(`测试图片最多上传 ${testLimits.image_max_count} 张`)
    return Upload.LIST_IGNORE
  }
  if (videoCount > testLimits.video_max_count) {
    message.error(`测试视频最多上传 ${testLimits.video_max_count} 个`)
    return Upload.LIST_IGNORE
  }
  testingFiles.value = [...testingFiles.value, file]
  testingUploadList.value = [
    ...testingUploadList.value,
    {
      uid: (file as any).uid || `${Date.now()}-${Math.random()}`,
      name: file.name,
      status: 'done',
      originFileObj: file,
    },
  ]
  return false
}

function onTestUploadRequest(options: any) {
  if (typeof options?.onSuccess === 'function') {
    options.onSuccess({}, options.file)
  }
}

function removeTestingFile(file: any) {
  const uid = String(file?.uid || file?.originFileObj?.uid || '')
  const raw = file?.originFileObj || file
  testingFiles.value = testingFiles.value.filter((item) => item !== raw)
  testingUploadList.value = testingUploadList.value.filter((item) => {
    if (uid && String(item?.uid || '') === uid) return false
    return (item?.originFileObj || item) !== raw
  })
}

function resetTestModalState() {
  stopPollingTestJob()
  revokeTestResultPreviewURLs()
  testingFiles.value = []
  testingUploadList.value = []
  testResults.value = []
  currentTestJobID.value = ''
  currentTestJobStatus.value = 'pending'
  currentTestAlgorithmID.value = ''
}

async function runTest() {
  if (!testingAlgorithm.value || testingFiles.value.length === 0) {
    message.warning('请先选择图片或视频')
    return
  }
  runningTest.value = true
  try {
    message.loading({
      content: '测试进行中，视频分析可能需要 3 分钟...',
      key: 'algorithm-test-running',
      duration: 0,
    })
      const formData = new FormData()
      for (const file of testingFiles.value) {
        formData.append('files', file)
      }
      const data = await algorithmAPI.test(testingAlgorithm.value.id, formData) as {
        job_id: string
        batch_id: string
        algorithm_id?: string
        status?: TestJobStatus
        total_count?: number
      }
      const jobID = String(data?.job_id || '').trim()
      if (!jobID) {
        throw new Error('创建测试任务失败：缺少 job_id')
      }
      currentTestJobID.value = jobID
      currentTestJobStatus.value = data?.status || 'pending'
      currentTestAlgorithmID.value = testingAlgorithm.value.id
      testResults.value = buildPendingBatchTestResults(testingFiles.value)
      message.success('测试任务已创建，正在后台分析')
      void startPollingTestJob(jobID)
    } catch (err) {
      message.error((err as Error).message)
    } finally {
      message.destroy('algorithm-test-running')
      runningTest.value = false
  }
}

async function loadTestRecords(algorithmID: string, page = 1) {
  historyLoading.value = true
  try {
    const data = await algorithmAPI.listTests(algorithmID, {
      page,
      page_size: testPager.page_size,
    }) as {
      items: TestRecord[]
      total: number
      page: number
      page_size: number
      total_pages: number
    }
    testRecords.value = data.items || []
    testPager.total = Number(data.total || 0)
    testPager.page = Number(data.page || 1)
    testPager.page_size = Number(data.page_size || 10)
    testPager.total_pages = Number(data.total_pages || 0)
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    historyLoading.value = false
  }
}

async function openTestHistory(row: Algorithm) {
  historyAlgorithm.value = row
  testHistoryModal.value = true
  testPager.page = 1
  await loadTestRecords(row.id, 1)
}

async function clearTestRecords() {
  if (!historyAlgorithm.value) return
  clearTestsLoading.value = true
  try {
    const result = await algorithmAPI.clearTests(historyAlgorithm.value.id) as {
      deleted_records?: number
      deleted_files?: number
    }
    const deletedRecords = Number(result?.deleted_records || 0)
    const deletedFiles = Number(result?.deleted_files || 0)
    message.success(`已清空测试记录 ${deletedRecords} 条，删除文件 ${deletedFiles} 个`)
    testPager.page = 1
    await loadTestRecords(historyAlgorithm.value.id, 1)
  } catch (err) {
    message.error((err as Error).message)
  } finally {
    clearTestsLoading.value = false
  }
}

function imageURL(path: string) {
  if (!path) return ''
  return mediaURL(path)
}

function mediaURL(path: string) {
  if (!path) return ''
  return withImageAuthURL(`${imageBase}${algorithmAPI.testMediaURL(path)}`)
}

function resolveImageURL(raw: string) {
  const path = String(raw || '').trim()
  if (!path) return ''
  if (path.startsWith('data:') || path.startsWith('blob:')) {
    return path
  }
  if (/^https?:\/\//i.test(path)) {
    return withImageAuthURL(path)
  }
  if (path.startsWith('/')) {
    return withImageAuthURL(`${imageBase}${path}`)
  }
  return withImageAuthURL(path)
}

function withImageAuthURL(url: string) {
  const target = String(url || '').trim()
  if (!target) return ''
  if (target.startsWith('/api/')) {
    return appendTokenQuery(target)
  }
  if (imageBase && target.startsWith(`${imageBase}/api/`)) {
    return appendTokenQuery(target)
  }
  const origin = window.location.origin
  if (target.startsWith(`${origin}/api/`)) {
    return appendTokenQuery(target)
  }
  return target
}

function revokeTestResultPreviewURLs() {
  for (const url of testResultPreviewURLs.value) {
    if (String(url || '').startsWith('blob:')) {
      URL.revokeObjectURL(url)
    }
  }
  testResultPreviewURLs.value = []
}

function buildPendingBatchTestResults(files: File[]) {
  revokeTestResultPreviewURLs()
  return files.map((file, index) => {
    const mediaType: 'image' | 'video' = String(file.type || '').startsWith('video/') ? 'video' : 'image'
    const previewURL = mediaType === 'image' ? URL.createObjectURL(file) : ''
    if (previewURL) {
      testResultPreviewURLs.value.push(previewURL)
    }
    const clientKey = `${file.name}-${index}-${file.size}-${file.lastModified}`
    return {
      client_key: clientKey,
      sort_order: index,
      status: 'pending' as const,
      job_item_id: '',
      record_id: '',
      file_name: file.name,
      media_type: mediaType,
      success: false,
      conclusion: '排队中',
      basis: '等待开始分析',
      media_url: '',
      preview_url: previewURL,
      normalized_boxes: [],
      anomaly_times: [],
      duration_seconds: undefined,
      error_message: '',
    } satisfies TestResultItem
  })
}

function mergeCurrentBatchTestResults(items: TestResultItem[]) {
  const existingByKey = new Map<string, TestResultItem>()
  for (const item of testResults.value) {
    const key = item.job_item_id || item.record_id || item.client_key || `${item.file_name}-${item.sort_order ?? ''}`
    existingByKey.set(key, item)
  }
  return items.map((item, index) => {
    const current: TestResultItem = { ...item }
    current.client_key = buildTestResultClientKey(item, index)
    const key = current.job_item_id || current.record_id || current.client_key || `${current.file_name}-${index}`
    const existing = existingByKey.get(key)
    if (existing?.preview_url) {
      current.preview_url = existing.preview_url
    } else {
      const uploadFile = testingFiles.value[index]
      if (uploadFile instanceof File && current.media_type === 'image') {
        const previewURL = URL.createObjectURL(uploadFile)
        current.preview_url = previewURL
        testResultPreviewURLs.value.push(previewURL)
      }
    }
    return current
  })
}

function isTestJobTerminal(status: TestJobStatus) {
  return status === 'completed' || status === 'partial_failed' || status === 'failed'
}

function stopPollingTestJob() {
  if (testJobPollTimer !== null) {
    window.clearTimeout(testJobPollTimer)
    testJobPollTimer = null
  }
}

async function pollTestJobOnce(jobID: string) {
  const snapshot = await algorithmAPI.getTestJob(jobID) as TestJobSnapshot
  applyTestJobSnapshot(snapshot)
  if (isTestJobTerminal(snapshot.status)) {
    stopPollingTestJob()
    if (historyAlgorithm.value?.id === snapshot.algorithm_id) {
      await loadTestRecords(snapshot.algorithm_id, testPager.page)
    }
    return
  }
  testJobPollTimer = window.setTimeout(() => {
    void pollTestJobOnce(jobID)
  }, 1500)
}

async function startPollingTestJob(jobID: string) {
  stopPollingTestJob()
  await pollTestJobOnce(jobID)
}

function applyTestJobSnapshot(snapshot: TestJobSnapshot) {
  currentTestJobID.value = snapshot.job_id
  currentTestJobStatus.value = snapshot.status
  currentTestAlgorithmID.value = snapshot.algorithm_id
  testResults.value = mergeCurrentBatchTestResults(Array.isArray(snapshot.items) ? snapshot.items : [])
}

function formatTestJobStatus(status?: string) {
  switch (status) {
    case 'pending':
      return { color: 'default', text: '排队中' }
    case 'running':
      return { color: 'processing', text: '分析中' }
    case 'success':
      return { color: 'green', text: '成功' }
    case 'failed':
      return { color: 'red', text: '失败' }
    default:
      return { color: 'default', text: '待处理' }
  }
}

function buildTestResultClientKey(item: TestResultItem, index: number) {
  const recordID = String(item.record_id || '').trim()
  if (recordID) return recordID
  const fileName = String(item.file_name || '').trim()
  if (fileName) return `${fileName}-${index}`
  return `result-${index}`
}

function resolveTestResultImageURL(item: TestResultItem) {
  const previewURL = resolveImageURL(item.preview_url || '')
  const remoteURL = resolveImageURL(item.media_url)
  return previewURL || remoteURL || ''
}

function parseSmallModelLabels(raw: string | string[]) {
  const source = Array.isArray(raw) ? raw : [String(raw || '')]
  const out: string[] = []
  for (const item of source) {
    const parts = String(item || '').split(',')
    for (const part of parts) {
      const label = part.trim()
      if (!label) continue
      if (!out.includes(label)) out.push(label)
    }
  }
  return out
}

function clampNumber(value: unknown, min: number, max: number, fallback: number) {
  const parsed = Number(value)
  if (!Number.isFinite(parsed)) return fallback
  if (parsed < min) return min
  if (parsed > max) return max
  return parsed
}

function formatYoloLabelName(label: string) {
  const raw = String(label || '').trim()
  if (!raw) return '-'
  return yoloLabelNameMap.value.get(raw.toLowerCase()) || raw
}

function normalizedBoxStyle(item: NormalizedBox) {
  const w = clamp01(item.w)
  const h = clamp01(item.h)
  const x = clamp01(item.x)
  const y = clamp01(item.y)
  const left = Math.max(0, x - w / 2)
  const top = Math.max(0, y - h / 2)
  return {
    left: `${left * 100}%`,
    top: `${top * 100}%`,
    width: `${w * 100}%`,
    height: `${h * 100}%`,
  }
}

function clamp01(v: number) {
  if (!Number.isFinite(v)) return 0
  if (v < 0) return 0
  if (v > 1) return 1
  return v
}

function openTestBoxModal(record: TestRecord) {
  const mediaPath = record.media_path || record.image_path
  if (!mediaPath || record.media_type === 'video') {
    message.warning('仅图片记录支持查看框选')
    return
  }
  testBoxImageURL.value = mediaURL(mediaPath)
  testBoxList.value = Array.isArray(record.normalized_boxes) ? record.normalized_boxes : []
  testBoxTitle.value = `框选预览 - ${record.id}`
  testBoxModal.value = true
}

function openMediaPreview(record: TestRecord) {
  const targetURL = record.media_url || mediaURL(record.media_path || record.image_path)
  if (!targetURL) {
    message.warning('该记录没有可预览的媒体文件')
    return
  }
  mediaPreviewType.value = record.media_type === 'video' ? 'video' : 'image'
  mediaPreviewTitle.value = `媒体预览 - ${record.file_name || record.original_file_name || record.id}`
  mediaPreviewURL.value = targetURL
  mediaPreviewModal.value = true
}

function openResultImagePreview(item: TestResultItem) {
  if (item.media_type !== 'image') return
  const previewURL = resolveTestResultImageURL(item)
  if (!previewURL) {
    message.warning('该结果没有可预览的图片')
    return
  }
  testBoxImageURL.value = previewURL
  testBoxList.value = Array.isArray(item.normalized_boxes) ? item.normalized_boxes : []
  testBoxTitle.value = `框选预览 - ${item.file_name}`
  testBoxModal.value = true
}

function openPayload(title: string, content: string) {
  payloadTitle.value = title
  payloadText.value = formatPayloadJSON(content)
  payloadModal.value = true
}

function formatPayloadJSON(content: string) {
  const raw = String(content || '').trim()
  if (!raw || raw === 'null') {
    return '{}'
  }
  let value: unknown = raw
  for (let i = 0; i < 2; i++) {
    if (typeof value !== 'string') break
    const current = value.trim()
    if (!current) return '{}'
    try {
      value = JSON.parse(current)
    } catch {
      return current
    }
  }
  if (typeof value === 'string') {
    return value
  }
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return raw
  }
}

function formatAnomalyTimes(items?: TestAnomalyTime[]) {
  if (!Array.isArray(items) || items.length === 0) {
    return '-'
  }
  return items
    .map((item) => `${item.timestamp_text || '-'}${item.reason ? ` ${item.reason}` : ''}`)
    .join('；')
}

function formatFileSize(size: number) {
  const value = Number(size || 0)
  if (value <= 0) return '0B'
  if (value >= 1024 * 1024 * 1024) {
    return `${(value / (1024 * 1024 * 1024)).toFixed(1)}GB`
  }
  if (value >= 1024 * 1024) {
    return `${(value / (1024 * 1024)).toFixed(0)}MB`
  }
  if (value >= 1024) {
    return `${(value / 1024).toFixed(0)}KB`
  }
  return `${value}B`
}

watch(testModal, (open) => {
  if (!open) {
    stopPollingTestJob()
  }
})

onMounted(loadAll)
onUnmounted(() => {
  stopPollingTestJob()
  revokeTestResultPreviewURLs()
})
</script>

<template>
  <div>
    <h2 class="page-title">算法管理</h2>
    <p class="page-subtitle">管理算法、提示词版本、算法测试与测试记录。</p>

    <a-card class="glass-card">
      <div class="table-toolbar">
        <a-space>
          <a-upload
            :show-upload-list="false"
            accept=".json,application/json"
            :custom-request="onImportUploadRequest"
            @change="onImportUploadChange"
          >
            <a-button :loading="importingAlgorithms">导入</a-button>
          </a-upload>
          <a-button v-if="isDevelopmentMode" type="primary" @click="openCreateAlgorithm">新增算法</a-button>
        </a-space>
        <a-button @click="loadAll">刷新</a-button>
      </div>
      <a-table :loading="loading" :data-source="algorithms" row-key="id" :pagination="{ pageSize: 8 }">
        <a-table-column title="封面" width="96">
          <template #default="{ record }">
            <a-image
              v-if="record.image_url"
              :src="resolveImageURL(record.image_url)"
              :width="68"
              :height="44"
              style="object-fit: cover; border-radius: 6px"
            />
            <span v-else>-</span>
          </template>
        </a-table-column>
        <a-table-column title="编码" data-index="code" width="140" />
        <a-table-column title="名称" data-index="name" />
        <a-table-column v-if="isDevelopmentMode" title="当前提示词">
          <template #default="{ record }">{{ record.active_prompt?.version || '-' }}</template>
        </a-table-column>
        <a-table-column title="启用">
          <template #default="{ record }">
            <a-tag :color="record.enabled ? 'green' : 'default'">{{ record.enabled ? '启用' : '停用' }}</a-tag>
          </template>
        </a-table-column>
        <a-table-column title="操作" width="420">
          <template #default="{ record }">
            <a-space>
              <a-button size="small" @click="openEditAlgorithm(record)">编辑</a-button>
              <a-button v-if="isDevelopmentMode" size="small" @click="openPromptManager(record)">提示词</a-button>
              <a-button v-if="isDevelopmentMode" size="small" @click="openTestModal(record)">测试</a-button>
              <a-button v-if="isDevelopmentMode" size="small" @click="openTestHistory(record)">测试记录</a-button>
              <a-popconfirm v-if="isDevelopmentMode" title="确定删除该算法？" @confirm="removeAlgorithm(record.id)">
                <a-button size="small" danger>删除</a-button>
              </a-popconfirm>
            </a-space>
          </template>
        </a-table-column>
      </a-table>
    </a-card>

    <a-modal v-model:open="algorithmModal" :title="editingAlgorithmID ? '编辑算法' : '新增算法'" width="720px" @ok="submitAlgorithm">
      <a-form layout="vertical">
        <a-row :gutter="12">
          <a-col :span="24">
            <a-form-item label="算法名称"><a-input v-model:value="algorithmForm.name" /></a-form-item>
          </a-col>
        </a-row>
        <a-row :gutter="12">
          <a-col :span="12">
            <a-form-item label="场景"><a-input v-model:value="algorithmForm.scene" /></a-form-item>
          </a-col>
          <a-col :span="12">
            <a-form-item label="分类"><a-input v-model:value="algorithmForm.category" /></a-form-item>
          </a-col>
        </a-row>
        <a-row :gutter="12">
          <a-col :span="12">
            <a-form-item label="启用"><a-switch v-model:checked="algorithmForm.enabled" /></a-form-item>
          </a-col>
        </a-row>

        <a-form-item label="封面图片">
          <a-space direction="vertical" style="width: 100%">
            <a-space>
              <a-upload :before-upload="onCoverUpload" :show-upload-list="false" accept="image/*">
                <a-button :loading="uploadingCover">上传图片</a-button>
              </a-upload>
              <a-button v-if="algorithmForm.image_url" @click="algorithmForm.image_url = ''">清除</a-button>
            </a-space>
            <a-image
              v-if="algorithmForm.image_url"
              :src="resolveImageURL(algorithmForm.image_url)"
              :width="128"
              :height="72"
              style="object-fit: cover; border-radius: 6px"
            />
            <a-typography-text v-else type="secondary">未上传封面</a-typography-text>
          </a-space>
        </a-form-item>

        <a-form-item label="描述"><a-textarea v-model:value="algorithmForm.description" :rows="2" /></a-form-item>
        <a-form-item v-if="isDevelopmentMode" label="算法编码(task_code)">
          <a-input v-model:value="algorithmForm.code" placeholder="例如 ALG_FIRE_SMOKE" />
        </a-form-item>

        <a-row v-if="isDevelopmentMode" :gutter="12">
          <a-col :span="algorithmForm.detect_mode === 2 ? 24 : 12">
            <a-form-item label="识别模式(detect_mode)">
              <a-select v-model:value="algorithmForm.detect_mode" :options="detectModeOptions" />
            </a-form-item>
          </a-col>
          <a-col v-if="algorithmForm.detect_mode !== 2" :span="12">
            <a-form-item label="Labels 触发模式">
              <a-select v-model:value="algorithmForm.labels_trigger_mode" :options="labelsTriggerModeOptions" />
            </a-form-item>
          </a-col>
        </a-row>

        <a-row v-if="isDevelopmentMode && algorithmForm.detect_mode !== 2" :gutter="12">
          <a-col :span="algorithmForm.detect_mode === 3 ? 12 : 24">
            <a-form-item label="小模型阈值">
              <a-input-number v-model:value="algorithmForm.yolo_threshold" :min="0.01" :max="0.99" :step="0.01" style="width: 100%" />
            </a-form-item>
          </a-col>
          <a-col v-if="algorithmForm.detect_mode === 3" :span="12">
            <a-form-item label="IOU 阈值">
              <a-input-number v-model:value="algorithmForm.iou_threshold" :min="0.1" :max="0.99" :step="0.01" style="width: 100%" />
            </a-form-item>
          </a-col>
        </a-row>

        <a-typography-text v-if="isDevelopmentMode && algorithmForm.detect_mode === 2" type="secondary">
          仅大模型模式下无需配置小模型阈值、IOU 阈值与小模型标签。
        </a-typography-text>

        <a-form-item v-if="isDevelopmentMode && algorithmForm.detect_mode !== 2" label="小模型标签">
          <a-select
            v-model:value="algorithmForm.small_model_label"
            mode="multiple"
            :options="labelOptions"
            placeholder="可选择多个标签"
          />
        </a-form-item>
      </a-form>
    </a-modal>

    <a-modal v-model:open="promptModal" title="Prompt Version Management" width="760px" :footer="null">
      <a-form layout="vertical">
        <a-form-item label="Version"><a-input v-model:value="promptForm.version" /></a-form-item>
        <a-form-item label="Prompt"><a-textarea v-model:value="promptForm.prompt" :rows="5" /></a-form-item>
        <a-button type="primary" @click="addPrompt">Add Version</a-button>
      </a-form>
      <a-divider />
      <a-list :data-source="prompts" bordered>
        <template #renderItem="{ item }">
          <a-list-item>
            <a-space direction="vertical" style="width: 100%">
              <a-space>
                <a-tag>{{ item.version }}</a-tag>
                <a-tag v-if="item.is_active" color="green">Active</a-tag>
                <a-button size="small" @click="activatePrompt(item.id)">Activate</a-button>
                <a-popconfirm
                  v-if="!item.is_active"
                  title="Delete this prompt version?"
                  @confirm="removePrompt(item.id)"
                >
                  <a-button size="small" danger>Delete</a-button>
                </a-popconfirm>
              </a-space>
              <div class="mono">{{ item.prompt }}</div>
            </a-space>
          </a-list-item>
        </template>
      </a-list>
    </a-modal>

    <a-modal v-model:open="testModal" title="算法测试" width="1080px" :confirm-loading="runningTest" @ok="runTest">
      <a-space direction="vertical" style="width: 100%">
        <a-upload
          multiple
          :file-list="testingUploadList"
          :show-upload-list="true"
          accept="image/*,video/*"
          :custom-request="onTestUploadRequest"
          :before-upload="onTestFileBeforeUpload"
          @remove="removeTestingFile"
        >
          <a-button>选择图片或视频</a-button>
        </a-upload>
        <a-typography-text type="secondary">
          已选文件：{{ testingFiles.length }}，图片最多 {{ testLimits.image_max_count }} 张，视频最多 {{ testLimits.video_max_count }} 个，视频大小不超过 {{ formatFileSize(testLimits.video_max_bytes) }}
        </a-typography-text>
      </a-space>
      <a-divider />
      <div v-if="testResults.length === 0" class="test-empty">暂无结果</div>
        <div v-else class="test-result-grid">
          <div v-for="item in testResults" :key="item.job_item_id || item.record_id || item.client_key || item.file_name" class="test-result-card">
            <div class="test-result-head">
              <div>
                <div class="test-result-title">{{ item.file_name }}</div>
                <a-tag :color="formatTestJobStatus(item.status).color">{{ formatTestJobStatus(item.status).text }}</a-tag>
                <a-tag>{{ item.media_type === 'video' ? '视频' : '图片' }}</a-tag>
              </div>
              <a-button v-if="item.media_type === 'image'" size="small" @click="openResultImagePreview(item)">查看框选</a-button>
            </div>

          <div v-if="item.media_type === 'image'" class="test-snapshot-wrap" :style="{ aspectRatio: testBoxAspectRatio }">
            <img
              v-if="resolveTestResultImageURL(item)"
              :src="resolveTestResultImageURL(item)"
              class="test-snapshot-image"
              :alt="item.file_name"
            />
            <div v-else class="test-snapshot-placeholder">该图片暂无可预览内容</div>
            <div
              v-for="(box, idx) in item.normalized_boxes || []"
              :key="`${box.label}-${idx}`"
              class="test-snapshot-box"
              :style="normalizedBoxStyle(box)"
            >
              <span class="test-snapshot-box-label">{{ formatYoloLabelName(box.label) }} {{ (Number(box.confidence || 0) * 100).toFixed(1) }}%</span>
            </div>
          </div>
          <video
            v-else-if="item.media_url"
            class="test-video"
            :src="resolveImageURL(item.media_url)"
            controls
            preload="metadata"
          />

          <div class="test-result-meta">
            <div><strong>识别结果：</strong>{{ item.conclusion || '-' }}</div>
            <div><strong>判定依据：</strong>{{ item.basis || '-' }}</div>
            <div v-if="(item.status === 'failed' || item.success === false) && item.error_message"><strong>失败原因：</strong>{{ item.error_message }}</div>
            <div v-if="item.media_type === 'video'"><strong>异常时间：</strong>{{ formatAnomalyTimes(item.anomaly_times) }}</div>
          </div>
        </div>
      </div>
    </a-modal>

    <a-modal v-model:open="testHistoryModal" :title="`测试记录 - ${historyAlgorithm?.name || ''}`" :footer="null" width="1020px">
      <div class="table-toolbar" style="margin-bottom: 8px; justify-content: flex-end">
        <a-popconfirm title="确定清空该算法的测试记录吗？" @confirm="clearTestRecords">
          <a-button danger size="small" :loading="clearTestsLoading" :disabled="historyLoading || testRecords.length === 0">清空记录</a-button>
        </a-popconfirm>
      </div>
      <a-table :data-source="testRecords" :loading="historyLoading" row-key="id" :pagination="false" size="small">
        <a-table-column title="时间" width="180">
          <template #default="{ record }">{{ formatDateTime(record.created_at) }}</template>
        </a-table-column>
        <a-table-column title="结果" width="90">
          <template #default="{ record }">
            <a-tag :color="record.success ? 'green' : 'red'">{{ record.success ? '成功' : '失败' }}</a-tag>
          </template>
        </a-table-column>
        <a-table-column title="结论">
          <template #default="{ record }">
            <div>{{ record.conclusion || (record.success ? '分析成功' : '分析失败') }}</div>
            <a-typography-text type="secondary">{{ record.basis || '-' }}</a-typography-text>
          </template>
        </a-table-column>
        <a-table-column title="媒体" width="140">
          <template #default="{ record }">
            <a-space>
              <a-tag>{{ record.media_type === 'video' ? '视频' : '图片' }}</a-tag>
              <a-button size="small" @click="openMediaPreview(record)">预览</a-button>
            </a-space>
          </template>
        </a-table-column>
        <a-table-column title="异常时间" width="220">
          <template #default="{ record }">{{ formatAnomalyTimes(record.anomaly_times) }}</template>
        </a-table-column>
        <a-table-column title="载荷" width="220">
          <template #default="{ record }">
            <a-space>
              <a-button size="small" :disabled="record.media_type === 'video'" @click="openTestBoxModal(record)">框选</a-button>
              <a-button size="small" @click="openPayload('请求参数', record.request_payload)">请求</a-button>
              <a-button size="small" @click="openPayload('响应结果', record.response_payload)">响应</a-button>
            </a-space>
          </template>
        </a-table-column>
        <a-table-column title="记录 ID" data-index="id" />
      </a-table>
      <div class="pager">
        <a-pagination
          size="small"
          :current="testPager.page"
          :page-size="testPager.page_size"
          :total="testPager.total"
          @change="(page: number) => historyAlgorithm && loadTestRecords(historyAlgorithm.id, page)"
        />
      </div>
    </a-modal>

    <a-modal v-model:open="testBoxModal" :title="testBoxTitle" :footer="null" width="920px">
      <div class="test-snapshot-wrap" :style="{ aspectRatio: testBoxAspectRatio }">
        <img :src="testBoxImageURL" class="test-snapshot-image" alt="测试图片" />
        <div
          v-for="(item, idx) in testBoxList"
          :key="`${item.label}-${idx}`"
          class="test-snapshot-box"
          :style="normalizedBoxStyle(item)"
        >
          <span class="test-snapshot-box-label">{{ formatYoloLabelName(item.label) }} {{ (Number(item.confidence || 0) * 100).toFixed(1) }}%</span>
        </div>
      </div>
      <a-typography-text type="secondary">框选数量：{{ testBoxList.length }}</a-typography-text>
    </a-modal>

    <a-modal v-model:open="payloadModal" :title="payloadTitle" :footer="null" width="840px">
      <pre class="test-block">{{ payloadText }}</pre>
    </a-modal>

    <a-modal v-model:open="mediaPreviewModal" :title="mediaPreviewTitle" :footer="null" width="920px">
      <a-image v-if="mediaPreviewType === 'image'" :src="mediaPreviewURL" style="width: 100%" />
      <video v-else :src="mediaPreviewURL" controls preload="metadata" style="width: 100%" />
    </a-modal>
  </div>
</template>

<style scoped>
.test-block {
  max-height: 320px;
  overflow: auto;
  background: #f7fbf3;
  border: 1px solid #d6e4c4;
  border-radius: 8px;
  padding: 10px;
  margin-top: 8px;
}

.test-empty {
  min-height: 120px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #8c8c8c;
  border: 1px dashed #d9d9d9;
  border-radius: 8px;
}

.test-result-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 16px;
}

.test-result-card {
  border: 1px solid #d9d9d9;
  border-radius: 12px;
  padding: 12px;
  background: #fff;
}

.test-result-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 10px;
}

.test-result-title {
  font-weight: 600;
  margin-bottom: 6px;
  word-break: break-all;
}

.test-result-meta {
  display: grid;
  gap: 6px;
  margin-top: 10px;
  font-size: 13px;
  line-height: 1.6;
}

.test-video {
  width: 100%;
  max-height: 260px;
  border-radius: 8px;
  background: #000;
}

.pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 10px;
}

.test-snapshot-wrap {
  position: relative;
  width: 100%;
  border: 1px solid #d9d9d9;
  border-radius: 8px;
  overflow: hidden;
  background: #000;
  margin-bottom: 8px;
}

.test-snapshot-image {
  width: 100%;
  height: 100%;
  object-fit: contain;
  display: block;
}

.test-snapshot-placeholder {
  width: 100%;
  height: 100%;
  min-height: 180px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: rgba(255, 255, 255, 0.72);
  background: linear-gradient(135deg, rgba(20, 20, 20, 0.92), rgba(48, 48, 48, 0.92));
  font-size: 13px;
}

.test-snapshot-box {
  position: absolute;
  border: 2px solid #ff4d4f;
  box-sizing: border-box;
}

.test-snapshot-box-label {
  position: absolute;
  left: 0;
  top: 0;
  transform: translateY(-100%);
  background: rgba(255, 77, 79, 0.9);
  color: #fff;
  font-size: 12px;
  line-height: 1.2;
  padding: 2px 6px;
  border-radius: 4px 4px 0 0;
  white-space: nowrap;
}
</style>
