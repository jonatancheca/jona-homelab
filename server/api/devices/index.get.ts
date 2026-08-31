import { getRuntime } from '../../utils/runtime'
import { apiHandler } from '../../utils/http'

export default apiHandler(() => getRuntime().store.list())
