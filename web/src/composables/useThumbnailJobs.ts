import { onBeforeUnmount, ref, watch } from 'vue'
import { api, ApiError } from '@/api/client'
import type { VideoThumbnailJob } from '@/types/files'

const active = (job: VideoThumbnailJob) => ['settling', 'queued', 'probing', 'generating', 'publishing'].includes(job.state)

function newer(left: VideoThumbnailJob, right: VideoThumbnailJob) {
  const milliseconds = Date.parse(left.updated_at) - Date.parse(right.updated_at)
  if (milliseconds !== 0) return milliseconds > 0
  const fraction = (value: string) => (value.match(/\.(\d+)(?:Z|[+-]\d{2}:\d{2})$/)?.[1] ?? '').padEnd(9, '0')
  return fraction(left.updated_at) > fraction(right.updated_at)
}

export function createThumbnailJobs() {
  const jobs = ref<VideoThumbnailJob[]>([])
  let loadRevision = 0
  let timer: ReturnType<typeof setInterval> | undefined
  let loading = false
  let disposed = false

  async function load() {
    const revision = ++loadRevision
    const before = new Map(jobs.value.map(job => [job.id, job]))
    const loaded = (await api.thumbnailJobs()).jobs
    if (disposed || revision !== loadRevision) return
    const current = new Map(jobs.value.map(job => [job.id, job]))
    const ids = new Set(loaded.map(job => job.id))
    jobs.value = [
      ...loaded.map(job => {
        const existing = current.get(job.id)
        return existing && newer(existing, job) ? existing : job
      }),
      // A list captured before a realtime update must not erase that update.
      ...jobs.value.filter(job => !ids.has(job.id) && before.get(job.id) !== job),
    ]
  }

  function update(job: VideoThumbnailJob) {
    const previous = jobs.value.find(current => current.id === job.id)
    if (previous && newer(previous, job)) return
    jobs.value = [job, ...jobs.value.filter(current => current.id !== job.id)]
  }

  async function cancel(id: string) {
    try {
      await api.cancelThumbnailJob(id)
    } catch (err) {
      if (!(err instanceof ApiError) || err.status !== 404) throw err
      await load()
      // Completion/removal can legitimately race with the user's click.
      if (jobs.value.some(job => job.id === id && active(job))) throw err
      return
    }
    await load()
  }

  watch(() => jobs.value.some(job => active(job) || ['ready', 'cancelled'].includes(job.state)), running => {
    if (timer !== undefined) clearInterval(timer)
    timer = running ? setInterval(async () => {
      if (loading) return
      loading = true
      try { await load() } catch { /* Reconnect and the next tick retry. */ }
      finally { loading = false }
    }, 5000) : undefined
  })
  onBeforeUnmount(() => {
    disposed = true
    if (timer !== undefined) clearInterval(timer)
  })
  return { jobs, load, update, cancel }
}
