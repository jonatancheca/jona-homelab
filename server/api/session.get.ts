import { getRuntime } from '../utils/runtime'
import { apiHandler } from '../utils/http'

export default apiHandler(() => ({ mode: getRuntime().settings.developmentBypass ? 'development' : 'access' }))
