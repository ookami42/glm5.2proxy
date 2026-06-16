export interface QuotaBalance {
  id: string
  model: string
  total: number | null
  used: number | null
  remaining: number | null
  available: number | null
  usagePercent: number | null
  periodEnd: string | null
}

export interface QuotaSnapshot {
  generatedAt: string
  balances: QuotaBalance[]
}

export interface Account {
  id: string
  label: string
  active: boolean
  queuePosition: number
  registrationOrder: number
  user: {
    id?: string
    email?: string
    name?: string
    avatar?: string
  }
  quota: QuotaSnapshot | null
  quotaError: { message: string; type: string } | null
}

export interface AccountsResponse {
  object: string
  activeAccountId: string | null
  data: Account[]
}

export interface APIKey {
  id: string
  name: string
  prefix: string
  createdAt: string
}

export type ThinkingEffort = 'none' | 'low' | 'medium' | 'high' | 'max'

export interface ThinkingSettings {
  enabled: boolean
  budgetTokens: number
  effort: ThinkingEffort
}

export interface Settings {
  version: number
  port: number
  apiEnabled: boolean
  apiKeyRequired: boolean
  globalThinking: ThinkingSettings
  accountThinking: Record<string, ThinkingSettings>
  apiKeys: APIKey[]
}
