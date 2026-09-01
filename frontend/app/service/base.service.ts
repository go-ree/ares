import config from '../../config'

export class BaseService {
  protected config = config

  protected async request<T>(url: string, options: RequestInit = {}): Promise<T> {
    const response = await fetch(`${this.config.apiBaseUrl}${url}`, {
      ...options,
      headers: {
        'Content-Type': 'application/json',
        ...options.headers,
      },
    })

    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`)
    }

    return response.json()
  }
} 