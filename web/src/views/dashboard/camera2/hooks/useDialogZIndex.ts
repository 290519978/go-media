
import { ref } from 'vue'

let baseZIndex = 2000
const currentZIndex = ref(baseZIndex)

export function useDialogZIndex() {
  const getNextZIndex = () => {
    currentZIndex.value++
    return currentZIndex.value
  }
  
  const resetZIndex = () => {
    currentZIndex.value = baseZIndex
  }

  return {
    getNextZIndex,
    resetZIndex,
    currentZIndex
  }
}