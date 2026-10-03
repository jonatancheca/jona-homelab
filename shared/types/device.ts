export interface Device {
  id: string
  name: string
  mac: string
  address: string | null
  sshUser: string | null
  remoteMethod: RemoteMethod
  companionConfigured: boolean
  createdAt: string
  updatedAt: string
  lastSentAt: string | null
}

export type RemoteMethod = 'ssh' | 'companion' | 'none'
export type PowerAction = 'shutdown' | 'sleep' | 'hibernate'
export interface PowerInput { action: PowerAction, force: boolean }

export interface DeviceInput {
  name: string
  mac: string
  address: string | null
  remoteMethod?: RemoteMethod
  sshUser: string | null
  companionCode?: string
}

export interface WakeResult {
  message: string
  device: Device
  retryAfter: number
}

export interface DeviceStatus {
  deviceId: string
  networkReachable: boolean
  remoteReady: boolean
  remoteMethod: RemoteMethod
  checkedAt: string
  companion?: CompanionStatus
}

export interface CompanionOperation {
  phase: 'idle' | 'checking' | 'scheduled' | 'downloading' | 'verifying' | 'installing' | 'restarting' | 'succeeded' | 'failed' | 'rolled-back'
  targetVersion?: string
  error?: string
  updatedAt?: string
}

export interface CompanionStatus {
  version: string | null
  displayVersion?: string
  latestVersion: string | null
  latestDisplayVersion?: string
  remoteUpdate: boolean
  state: 'current' | 'available' | 'updating' | 'unknown' | 'local' | 'failed'
  releaseCheckedAt?: string
  releaseError?: string
  operation?: CompanionOperation
}

export interface CompanionUpdateResult {
  scheduled: boolean
  localBuild?: boolean
  targetVersion?: string
}

export interface ShutdownResult {
  message: string
  retryAfter: number
}
