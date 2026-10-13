import { ref } from 'vue'
import { api } from '@/api/client'
import { previewKindForContentType, previewKindForPath, type PreviewKind } from '@/utils/preview'

export function useFilePreview() {
  const visible = ref(false)
  const path = ref('')
  const kind = ref<PreviewKind>('unsupported')
  const contentType = ref('')
  let navigationRequest = 0

  async function detect(candidate: string) {
    let detectedKind = previewKindForPath(candidate)
    let detectedContentType = ''
    try {
      detectedContentType = (await api.contentType(candidate)).content_type
      detectedKind = previewKindForContentType(detectedContentType, candidate)
    } catch {
      // Extension mapping remains a fallback if type inspection is unavailable.
    }
    return { kind: detectedKind, contentType: detectedContentType }
  }

  function show(candidate: string, detected: { kind: PreviewKind; contentType: string }) {
    navigationRequest++
    path.value = candidate
    kind.value = detected.kind
    contentType.value = detected.contentType
    visible.value = true
  }

  async function navigate(candidate: string) {
    const request = ++navigationRequest
    const detected = await detect(candidate)
    if (request !== navigationRequest) return false
    show(candidate, detected)
    return true
  }

  function hide() {
    navigationRequest++
    visible.value = false
  }

  return { visible, path, kind, contentType, detect, show, navigate, hide }
}
