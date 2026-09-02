import { checkDevicesStatus } from '../../core/remote.ts'
import { getRuntime } from '../../utils/runtime'
import { apiHandler } from '../../utils/http'

export default apiHandler(() => {
  const { store, settings } = getRuntime()
  return checkDevicesStatus(store.list(), settings.ssh)
})
