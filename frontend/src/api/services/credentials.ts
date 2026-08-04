// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Credentials service. Wraps /api/v1/credentials (admin CRUD).
// v0.8.2 surface:
//
//   - GET    /                       -> list every credential
//   - GET    /{id}                   -> get a single credential
//   - POST   /                       -> create a credential
//   - PATCH  /{id}                   -> rotate the credential_value
//   - DELETE /{id}                   -> hard delete
//   - GET    /by-user/{userId}       -> list every credential for a user
//   - GET    /by-inbound/{ibId}      -> list every credential for an inbound
//
// The shape mirrors the OpenAPI spec
// (`docs/openapi.yaml` -> `Credential` schema) one-to-one
// after the camelizeKeys transform. The wire format is
// the camelCase `Credential` type from `@/types/aegis`.
//
// # Why no zod schema (yet)
//
// The `users`, `hosts`, `nodes` services have a zod
// schema in `src/schemas/<entity>.ts` for the create /
// update request shapes. Credentials gets a zod schema
// in v0.8.x when the CredentialsView (this PR) lands —
// the schema file lives alongside `src/schemas/
// credential.ts` and the request shapes here will
// re-export it.

import type { Credential, UUID } from '@/types'

import { api } from './../client'

// CreateCredentialRequest is the POST / body. The id
// and timestamps are NOT accepted from the caller —
// the server generates them.
export interface CreateCredentialRequest {
  userId: UUID
  inboundId: UUID
  credentialValue: string
}

// RotateCredentialRequest is the PATCH /{id} body.
// The (userId, inboundId) pair is fixed by the
// existing row; PATCH only takes the new
// credentialValue.
export interface RotateCredentialRequest {
  credentialValue: string
}

export async function listCredentials(): Promise<Credential[]> {
  const { data } = await api.get<{ credentials: Credential[] }>(
    '/api/v1/credentials/',
  )
  return data.credentials ?? []
}

export async function getCredential(id: UUID): Promise<Credential> {
  const { data } = await api.get<Credential>(`/api/v1/credentials/${id}`)
  return data
}

export async function createCredential(
  req: CreateCredentialRequest,
): Promise<Credential> {
  const { data } = await api.post<Credential>('/api/v1/credentials/', req)
  return data
}

export async function rotateCredential(
  id: UUID,
  req: RotateCredentialRequest,
): Promise<Credential> {
  const { data } = await api.patch<Credential>(
    `/api/v1/credentials/${id}`,
    req,
  )
  return data
}

export async function deleteCredential(id: UUID): Promise<void> {
  await api.delete(`/api/v1/credentials/${id}`)
}

export async function listCredentialsByUser(
  userId: UUID,
): Promise<Credential[]> {
  const { data } = await api.get<{ credentials: Credential[] }>(
    `/api/v1/credentials/by-user/${userId}`,
  )
  return data.credentials ?? []
}

export async function listCredentialsByInbound(
  inboundId: UUID,
): Promise<Credential[]> {
  const { data } = await api.get<{ credentials: Credential[] }>(
    `/api/v1/credentials/by-inbound/${inboundId}`,
  )
  return data.credentials ?? []
}
