// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Node CRUD schemas. The Go model lives in
// `backend/internal/nodes/`; the zod schemas here
// validate the create / update payloads the admin UI
// sends over the v1 API.
//
// State is a closed set (see node.go) — using zod's
// `z.enum` rather than `z.string` catches typos at
// the form layer.

import { z } from "zod";

import { hostPortSchema, tagSchema } from "./primitives";

export const nodeStateSchema = z.enum([
  "new",
  "online",
  "draining",
  "offline",
  "disabled",
]);

/** Fields the operator sets when creating a node. The
 * `id`, `state`, and timestamps are server-side.
 */
export const nodeCreateSchema = z.object({
  name: z.string().min(1).max(64),
  region: z.string().min(1).max(32),
  capacityHint: z.string().max(64).optional(),
  address: hostPortSchema,
  tags: z.array(tagSchema).max(16).optional(),
});

export type NodeCreateInput = z.infer<typeof nodeCreateSchema>;

/** Fields the operator may patch. The Go Service
 * rejects unknown fields, so we use `.strict()` to
 * surface typos early.
 */
export const nodeUpdateSchema = nodeCreateSchema.partial().strict();

export type NodeUpdateInput = z.infer<typeof nodeUpdateSchema>;

/** TofuPolicy is the closed set of SSH host-key trust
 * policies the panel accepts (see
 * `backend/internal/bootstrap/state.go`).
 */
export const tofuPolicySchema = z.enum(["reject", "accept-and-append"]);

/** AuthMethod is the v0.8.x UI discriminator for the
 * provision form's three-way radio (key / password /
 * stored). The value is the form's local state; the
 * wire payload sent to the Go API does NOT include it
 * (the wire format is the two-field XOR
 * `ssh_private_key` / `ssh_password`, both optional).
 *
 * - `key`: operator pastes a private key (PEM). The
 *   form maps this to `ssh_private_key` in the wire
 *   payload.
 * - `password`: operator pastes the VPS root
 *   password for first-time auth. The form maps this
 *   to `ssh_password` in the wire payload.
 * - `stored`: the panel re-uses the SSH key it
 *   generated on the first password-based install
 *   (migration 0020, sealed with the operator's age
 *   envelope, see `internal/crypto/envelope`). The
 *   form sends an empty auth object; the Go
 *   provisioner decrypts the stored key and uses it
 *   for the install. The radio option is disabled
 *   for first-time installs (state `new`); it is
 *   enabled for re-provisions (state `offline`,
 *   which is the only state a previously-provisioned
 *   node can be in per the v8.x state machine).
 */
export const authMethodSchema = z.enum(["key", "password", "stored"]);

/** Fields the operator sets when provisioning a node.
 * v0.8.x schema: the `authMethod` radio is the new
 * entry point; the SSH key / password fields are
 * rendered conditionally and validated against the
 * selected method. The wire payload sent to the Go
 * API is the two-field XOR `ssh_private_key` /
 * `ssh_password` (the Go handler enforces the same
 * XOR at the HTTP layer — see
 * `backend/internal/bootstrap/handler.go`).
 *
 * The form layer also enforces the
 * `expected_fingerprint` requirement when
 * `tofu_policy === 'reject'` (cross-field rule, not a
 * per-field rule).
 */
export const nodeProvisionSchema = z
  .object({
    authMethod: authMethodSchema,
    // Both auth fields are optional at the schema
    // level. The superRefine below enforces the
    // conditional-required + XOR rules based on the
    // selected authMethod.
    ssh_private_key: z.string().optional(),
    ssh_password: z.string().optional(),
    ssh_port: z
      .number()
      .int()
      .min(1, "ssh_port must be 1..65535")
      .max(65535, "ssh_port must be 1..65535")
      .optional(),
    ssh_user: z.string().max(64).optional(),
    tofu_policy: tofuPolicySchema.optional(),
    expected_fingerprint: z.string().max(200).optional(),
  })
  .superRefine((value, ctx) => {
    // v0.8.x: XOR + conditional-required based on
    // the auth method. The Go handler also enforces
    // the XOR (a defence-in-depth check at the HTTP
    // layer); the form check is for UX (the operator
    // gets the error in their own time, not after a
    // 502 round-trip).
    if (value.authMethod === "key") {
      if (!value.ssh_private_key || value.ssh_private_key.trim() === "") {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["ssh_private_key"],
          message:
            'ssh_private_key is required when auth_method is "key" (PEM, no passphrase).',
        });
      }
      if (value.ssh_password && value.ssh_password !== "") {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["ssh_password"],
          message:
            'ssh_password must be empty when auth_method is "key" (XOR with ssh_private_key).',
        });
      }
    } else if (value.authMethod === "password") {
      if (!value.ssh_password || value.ssh_password === "") {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["ssh_password"],
          message:
            'ssh_password is required when auth_method is "password" (the VPS root password).',
        });
      }
      if (value.ssh_private_key && value.ssh_private_key.trim() !== "") {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["ssh_private_key"],
          message:
            'ssh_private_key must be empty when auth_method is "password" (XOR with ssh_password).',
        });
      }
    } else {
      // authMethod === 'stored': the panel re-uses
      // its own key, so both fields must be empty.
      // The form disables the inputs in this mode
      // (so the operator cannot type), but the
      // schema check is here for defence-in-depth
      // (a custom form integration could submit
      // stale values).
      if (value.ssh_private_key && value.ssh_private_key.trim() !== "") {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["ssh_private_key"],
          message:
            'ssh_private_key must be empty when auth_method is "stored" (the panel re-uses its own key).',
        });
      }
      if (value.ssh_password && value.ssh_password !== "") {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["ssh_password"],
          message:
            'ssh_password must be empty when auth_method is "stored" (the panel re-uses its own key).',
        });
      }
    }
    // The Go handler accepts empty / omitted `tofu_policy`
    // and treats it as `reject`. When the policy is
    // `reject`, the operator must paste the fingerprint
    // so the panel does not silently trust an unknown
    // host key. When the policy is `accept-and-append`,
    // the fingerprint is ignored (the panel records the
    // observed one instead).
    if (
      (value.tofu_policy === undefined || value.tofu_policy === "reject") &&
      (!value.expected_fingerprint || value.expected_fingerprint.trim() === "")
    ) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["expected_fingerprint"],
        message:
          'expected_fingerprint is required when tofu_policy is "reject" (the safe default). Either paste the SHA256 fingerprint or switch tofu_policy to "accept-and-append".',
      });
    }
  });

export type NodeProvisionInput = z.infer<typeof nodeProvisionSchema>;

/**
 * Schema for the v0.8.12+ merged "Add node + provision"
 * dialog. The form is the v0.8.x create form with an
 * optional "Provision after create" section. When
 * `provisionNow` is `true`, the form sends
 * `POST /api/v1/nodes` (create), then
 * `POST /api/v1/nodes/{id}/provision` (provision)
 * in sequence — the second call reuses the auth
 * fields below. The `stored` auth method is
 * rejected at the schema level for first-time
 * installs (the panel has no key on file yet for
 * a node in state `new`); the form surfaces the
 * same constraint via the radio's disabled state.
 *
 * The conditional-required + XOR + tofu_policy rules
 * below mirror `nodeProvisionSchema` (DRY: a
 * helper enforces the same rules on the same fields
 * when `provisionNow` is on).
 */
export const nodeAddSchema = z
  .object({
    // nodeCreateSchema fields (inlined for shape
    // access; nodeCreateSchema itself is exported
    // for callers that only need the create subset).
    name: z.string().min(1).max(64),
    region: z.string().min(1).max(32),
    capacityHint: z.string().max(64).optional(),
    address: hostPortSchema,
    tags: z.array(tagSchema).max(16).optional(),
    // v0.8.12: provision discriminator + auth fields.
    // All optional at the schema level when
    // `provisionNow` is `false`; conditionally
    // required / XOR-validated by the superRefine
    // below when `provisionNow` is `true`.
    provisionNow: z.boolean().default(true),
    authMethod: authMethodSchema.optional(),
    ssh_private_key: z.string().optional(),
    ssh_password: z.string().optional(),
    ssh_port: z
      .number()
      .int()
      .min(1, "ssh_port must be 1..65535")
      .max(65535, "ssh_port must be 1..65535")
      .optional(),
    ssh_user: z.string().max(64).optional(),
    tofu_policy: tofuPolicySchema.optional(),
    expected_fingerprint: z.string().max(200).optional(),
  })
  .superRefine((value, ctx) => {
    if (!value.provisionNow) return;
    // `stored` is for re-provisions (the panel has a
    // key on file from a prior install). For
    // first-time installs the panel has no key yet.
    if (value.authMethod === "stored") {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["authMethod"],
        message:
          "'Stored panel key' is for re-provisions only. Use 'key' or 'password' for the first install.",
      });
      return;
    }
    if (value.authMethod === "key") {
      if (!value.ssh_private_key || value.ssh_private_key.trim() === "") {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["ssh_private_key"],
          message:
            'ssh_private_key is required when auth_method is "key" (PEM, no passphrase).',
        });
      }
      if (value.ssh_password && value.ssh_password !== "") {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["ssh_password"],
          message:
            'ssh_password must be empty when auth_method is "key" (XOR with ssh_private_key).',
        });
      }
    } else if (value.authMethod === "password") {
      if (!value.ssh_password || value.ssh_password === "") {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["ssh_password"],
          message:
            'ssh_password is required when auth_method is "password" (the VPS root password).',
        });
      }
      if (value.ssh_private_key && value.ssh_private_key.trim() !== "") {
        ctx.addIssue({
          code: z.ZodIssueCode.custom,
          path: ["ssh_private_key"],
          message:
            'ssh_private_key must be empty when auth_method is "password" (XOR with ssh_password).',
        });
      }
    }
    if (
      (value.tofu_policy === undefined || value.tofu_policy === "reject") &&
      (!value.expected_fingerprint || value.expected_fingerprint.trim() === "")
    ) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ["expected_fingerprint"],
        message:
          'expected_fingerprint is required when tofu_policy is "reject". Either paste the SHA256 fingerprint or switch tofu_policy to "accept-and-append".',
      });
    }
  });

export type NodeAddInput = z.infer<typeof nodeAddSchema>;

/**
 * Schema for the v0.8.4 rotate-panel-key dialog.
 * The form is a single textarea (the operator's
 * existing SSH private key, PEM-encoded, no
 * passphrase) plus the optional ssh_port /
 * ssh_user overrides. The PEM is the only
 * required field — the rest fall back to the
 * panel's service-wide defaults (root@:22).
 *
 * The wire payload (what the form's onSubmit
 * posts to `/api/v1/nodes/{id}/rotate-panel-key`)
 * is the `NodeRotatePanelKeyRequest` shape from
 * `api/services/nodes.ts`; the zod schema here
 * validates the form's local state, which is the
 * same shape minus the codegen drift.
 *
 * v0.8.x auth-method context: the
 * rotate-panel-key flow is NOT the v0.8.1
 * "first-time install" path. The endpoint is the
 * v0.3.0..v0.7.x re-provision escape hatch:
 * the node already has the operator's PEM
 * authorised in $HOME/.ssh/authorized_keys from
 * the original install. A password would only be
 * meaningful on a brand-new node that does not
 * yet have any keys — that path is the
 * `POST /{id}/provision` endpoint, not this
 * one. The form therefore has no auth-method
 * radio: a private key is the only legal auth
 * method here.
 */
export const nodeRotatePanelKeySchema = z.object({
  ssh_private_key: z
    .string()
    .min(
      1,
      "ssh_private_key is required (paste the operator PEM the panel used to install this node)",
    ),
  ssh_port: z
    .number()
    .int()
    .min(1, "ssh_port must be 1..65535")
    .max(65535, "ssh_port must be 1..65535")
    .optional(),
  ssh_user: z.string().max(64).optional(),
});

export type NodeRotatePanelKeyInput = z.infer<typeof nodeRotatePanelKeySchema>;

// v0.8.7: refreshNodeAgentBearer is the
// operator-side recovery path for the
// agent bearer. The body is optional;
// the panel fills in defaults from the
// node's stored `Address` and the
// service-wide `cfg.AgentSSHUser`. The
// schema is `strict()` so unknown
// fields (e.g. an operator who pastes
// the v0.8.4 rotate-panel-key shape)
// fail loudly rather than silently
// dropping the extra fields.
export const nodeRefreshAgentBearerSchema = z
  .object({
    ssh_port: z
      .number()
      .int()
      .min(1, "ssh_port must be 1..65535")
      .max(65535, "ssh_port must be 1..65535")
      .optional(),
    ssh_user: z.string().max(64).optional(),
  })
  .strict();

export type NodeRefreshAgentBearerInput = z.infer<typeof nodeRefreshAgentBearerSchema>;
