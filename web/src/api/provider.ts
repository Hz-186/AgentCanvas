import { api } from './client';
import type {
  CreateProviderRequest,
  ModelProvider,
  UpdateProviderRequest,
} from '../types/api';

export const providerApi = {
  list: () => api.get<ModelProvider[]>('/model-providers'),
  get: (id: number) => api.get<ModelProvider>(`/model-providers/${id}`),
  create: (body: CreateProviderRequest) =>
    api.post<ModelProvider>('/model-providers', body),
  update: (id: number, body: UpdateProviderRequest) =>
    api.patch<ModelProvider>(`/model-providers/${id}`, body),
  remove: (id: number) =>
    api.delete<{ success: boolean }>(`/model-providers/${id}`),
  test: (id: number) => api.post<ModelProvider>(`/model-providers/${id}/test`),
};
