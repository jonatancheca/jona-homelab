import { initializeRuntime } from '../utils/runtime'

export default defineNitroPlugin((nitro) => {
  const runtime = initializeRuntime(import.meta.dev)
  nitro.hooks.hook('close', () => runtime.store.close())
})
