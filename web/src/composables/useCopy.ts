import { ref } from 'vue'
export function useCopy() {
  const feedback = ref('')
  async function copy(text: string) {
    try {
      await navigator.clipboard.writeText(text)
      feedback.value = 'Copied to clipboard.'
    } catch {
      feedback.value = 'Could not copy. Select the text and copy it manually.'
    }
  }
  return { copy, feedback }
}
