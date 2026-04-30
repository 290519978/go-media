/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'

  const component: DefineComponent<Record<string, never>, Record<string, never>, any>
  export default component
}

declare global {
  interface Window {
    Jessibuca?: new (config: Record<string, unknown>) => {
      play: (url: string) => void | Promise<void>
      destroy: () => void | Promise<void>
      on: (event: string, cb: (...args: unknown[]) => void) => void
    }
  }
}

export {}
