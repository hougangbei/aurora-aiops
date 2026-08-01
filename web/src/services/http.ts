import axios from 'axios';

// 平台认证使用 HttpOnly Session Cookie；Axios 必须同源携带 Cookie，
// 不读取、不写入任何 Kubernetes Token。
export const http = axios.create({
  baseURL: '/api/v1',
  timeout: 15000,
  withCredentials: true,
});
