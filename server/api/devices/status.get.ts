import { checkDevicesStatus } from '../../core/remote.ts'
import { getRuntime } from '../../utils/runtime'
import { apiHandler } from '../../utils/http'
import { latestCompanionRelease, withCompanionRelease } from '../../core/companion-updates.ts'

export default apiHandler(async () => {
  const { store, settings } = getRuntime()
  const devices = store.list()
  const statuses = await checkDevicesStatus(devices, settings.ssh, device => store.companionSecretOrNull(device.id))
  const result = await Promise.all(statuses.map(async (status, index) => {
    const recorded = store.recordStatus(devices[index]!, status)
    if (!recorded?.companion) return recorded
    return withCompanionRelease(recorded, await latestCompanionRelease(recorded.companion.platform))
  }))
  return result.filter(status => status != null)
})
