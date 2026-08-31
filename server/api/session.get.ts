import { apiHandler } from '../utils/http'

export default apiHandler(() => ({ mode: import.meta.dev ? 'development' : 'access' }))
