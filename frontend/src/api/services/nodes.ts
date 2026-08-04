// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Nodes service. Wraps the /api/v1/nodes CRUD endpoints.
// v0.1.0 ships list/get/create/update/delete.
// v0.3.0 adds provision (the BYO Node bootstrap
// action; backed by `internal/bootstrap`).
// v0.8.4 adds rotateNodePanelKey (the HTTP mirror
// of the v0.8.3 `aegis admin node rotate-panel-key`
// CLI; backed by `internal/bootstrap.HandleRotatePanelKey`).

import type {
  Node,
  NodeCreateRequest as NodeCreateRequestBase,
  NodeProvisionRequest,
  NodeProvisionResponse,
  NodeRotatePanelKeyRequest,
  NodeRotatePanelKeyResponse,
  UUID,
} from "@/types";

import { api } from "../client";

// NodeCreateRequest is the v0.1.0 wire shape
// for the create endpoint. Defined here (not
// in @/types) because the rest of the
// frontend only consumes it as a one-shot
// service argument; the Node type itself is
// the persisted shape.
export type NodeCreateRequest = NodeCreateRequestBase;

// NodeUpdateRequest is the PATCH shape: every
// field optional (a no-op PATCH is a legal
// call). Matches the Go
// `updateRequest` struct in
// `backend/internal/nodes/handler.go`.
export type NodeUpdateRequest = Partial<NodeCreateRequest>;

export async function listNodes(): Promise<Node[]> {
  // The panel's /api/v1/nodes/ endpoint returns
  // `{ nodes: Node[] }` rather than a bare array, so
  // unwrap before handing the list back to callers.
  // (Same shape used by listHosts / listUsers / listAudits.)
  const { data } = await api.get<{ nodes: Node[] }>("/api/v1/nodes/");
  return data.nodes ?? [];
}

export async function getNode(id: UUID): Promise<Node> {
  const { data } = await api.get<Node>(`/api/v1/nodes/${id}`);
  return data;
}

export async function createNode(req: NodeCreateRequest): Promise<Node> {
  const { data } = await api.post<Node>("/api/v1/nodes/", req);
  return data;
}

export async function updateNode(
  id: UUID,
  req: NodeUpdateRequest,
): Promise<Node> {
  const { data } = await api.put<Node>(`/api/v1/nodes/${id}`, req);
  return data;
}

export async function deleteNode(id: UUID): Promise<void> {
  await api.delete(`/api/v1/nodes/${id}`);
}

/**
 * Provision a node (v0.3.0 BYO Node flow). Wraps
 * `POST /api/v1/nodes/{id}/provision`. Synchronous
 * in v0.3.0 — the panel runs the install to
 * completion before returning. v0.5.0 will move to
 * kick-off+poll (returns 202 + a job id) for
 * large fleets; this signature stays stable.
 *
 * Throws on 400 / 404 / 409 / 502 — the UI
 * distinguishes by `toApiError(error).message`.
 */
export async function provisionNode(
  id: UUID,
  req: NodeProvisionRequest,
): Promise<NodeProvisionResponse> {
  const { data } = await api.post<NodeProvisionResponse>(
    `/api/v1/nodes/${id}/provision`,
    req,
  );
  return data;
}

/**
 * Rotate the panel's persistent SSH key on a node
 * (v0.8.4 HTTP mirror of the v0.8.3 CLI). Wraps
 * `POST /api/v1/nodes/{id}/rotate-panel-key`.
 *
 * The endpoint generates a fresh ed25519 keypair
 * on the panel, encrypts the private half with
 * the operator's age envelope, persists the
 * ciphertext, and pushes the public half to the
 * node's authorized_keys via SFTP. After the
 * call returns, the next re-provision on the
 * node (via the provision dialog, with no auth
 * input from the operator) decrypts and reuses
 * the new key — the "auto-deploy" experience
 * becomes available retroactively on
 * v0.3.0..v0.7.x nodes.
 *
 * Synchronous (the panel blocks on the SSH
 * handshake + SFTP push + remote shell). The 200
 * body carries the public key line and SHA256
 * fingerprint so the operator can verify what
 * is now in the node's authorized_keys.
 *
 * Throws on 400 (missing key, malformed body),
 * 404 (node not found), 500 (panel has no
 * envelope), 502 (SSH-side failure).
 */
export async function rotateNodePanelKey(
  id: UUID,
  req: NodeRotatePanelKeyRequest,
): Promise<NodeRotatePanelKeyResponse> {
  const { data } = await api.post<NodeRotatePanelKeyResponse>(
    `/api/v1/nodes/${id}/rotate-panel-key`,
    req,
  );
  return data;
}
