import axios, { type AxiosRequestConfig } from 'axios'

export type ApiResponse<T> = {
  code: number
  msg: string
  data: T
}

const instance = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || '',
  timeout: 15000,
})

export function appendTokenQuery(rawURL: string): string {
  const url = String(rawURL || '').trim()
  if (!url) return ''
  if (/^(data|blob):/i.test(url)) return url
  if (/(?:\?|&)token=/.test(url)) return url
  const token = localStorage.getItem('mb_token')
  if (!token) return url
  const hashIndex = url.indexOf('#')
  const base = hashIndex >= 0 ? url.slice(0, hashIndex) : url
  const hash = hashIndex >= 0 ? url.slice(hashIndex) : ''
  const sep = base.includes('?') ? '&' : '?'
  return `${base}${sep}token=${encodeURIComponent(token)}${hash}`
}

instance.interceptors.request.use((config) => {
  const token = localStorage.getItem('mb_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

function unwrap<T>(payload: ApiResponse<T>): T {
  if (payload.code === 0) return payload.data
  throw new Error(payload.msg || '请求失败')
}

async function get<T>(url: string, config?: AxiosRequestConfig): Promise<T> {
  try {
    const res = await instance.get<ApiResponse<T>>(url, config)
    return unwrap(res.data)
  } catch (error: any) {
    throw new Error(error?.response?.data?.msg || error?.message || '网络异常')
  }
}

async function post<T>(url: string, payload?: unknown, config?: AxiosRequestConfig): Promise<T> {
  try {
    const res = await instance.post<ApiResponse<T>>(url, payload, config)
    return unwrap(res.data)
  } catch (error: any) {
    throw new Error(error?.response?.data?.msg || error?.message || '网络异常')
  }
}

async function put<T>(url: string, payload?: unknown, config?: AxiosRequestConfig): Promise<T> {
  try {
    const res = await instance.put<ApiResponse<T>>(url, payload, config)
    return unwrap(res.data)
  } catch (error: any) {
    throw new Error(error?.response?.data?.msg || error?.message || '网络异常')
  }
}

async function del<T>(url: string, config?: AxiosRequestConfig): Promise<T> {
  try {
    const res = await instance.delete<ApiResponse<T>>(url, config)
    return unwrap(res.data)
  } catch (error: any) {
    throw new Error(error?.response?.data?.msg || error?.message || '网络异常')
  }
}

export default {
  get,
  post,
  put,
  delete: del,
}
