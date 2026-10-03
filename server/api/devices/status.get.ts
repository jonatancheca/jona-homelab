import { checkDevicesStatus } from '../../core/remote.ts'
import { getRuntime } from '../../utils/runtime'
import { apiHandler } from '../../utils/http'
import { latestCompanionRelease, withCompanionRelease } from '../../core/companion-updates.ts'

export default apiHandler(async () => {
  const { store, settings } = getRuntime()
  const devices = store.list()
  const [statuses, release] = await Promise.all([
    checkDevicesStatus(devices, settings.ssh, device => store.companionSecretOrNull(device.id)),
    devices.some(device => device.remoteMethod === 'companion') ? latestCompanionRelease() : Promise.resolve(null),
  ])
  return statuses.flatMap((status, index) => {
    const recorded = store.recordStatus(devices[index]!, status)
    return recorded ? [release ? withCompanionRelease(recorded, release) : recorded] : []
  })
})
