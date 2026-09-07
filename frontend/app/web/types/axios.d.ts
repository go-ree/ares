import 'axios';

declare module 'axios' {
  export interface AxiosRequestConfig {
    skipAuthHandling?: boolean;
    skipCsrf?: boolean;
  }

  export interface InternalAxiosRequestConfig {
    skipAuthHandling?: boolean;
    skipCsrf?: boolean;
  }
}
