// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Vitest tests for NodeInspectDialog. The test
// scope is smoke + key flows (mount, render,
// fire-and-forget GET on open, success card,
// empty state, error state) per the PR #270
// brief. Edge cases (e.g. retry behaviour) are
// covered by the parent view's manual QA.

// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'

import NodeInspectDialog from './NodeInspectDialog.vue'
import type { Node, NodeStoredKey } from '@/types'
import { getStoredNodeKey } from '@/api/services'
import { makeI18n, uiStubs } from './__test-helpers'

vi.mock('@/api/services', () => ({
  getStoredNodeKey: vi.fn(),
}))

const testNode: Node = {
  id: '550e8400-e29b-41d4-a716-446655440000',
  name: 'inspect-target',
  region: 'us-east',
  state: 'online',
  address: '10.0.0.1:22',
  capacityHint: '100',
  createdAt: '2026-08-01T10:00:00Z',
  updatedAt: '2026-08-01T10:00:00Z',
}

const storedKey: NodeStoredKey = {
  has_stored_key: true,
  public_key_line: 'ssh-ed25519 AAAA... aegis-panel@node-inspect-target',
  fingerprint: 'SHA256:abc123def456',
  algorithm: 'ed25519',
  key_updated_at: '2026-08-01T10:00:00Z',
}

const emptyStoredKey: NodeStoredKey = {
  has_stored_key: false,
}

describe('NodeInspectDialog', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.resetAllMocks()
  })

  it('mounts without crashing when closed', () => {
    const wrapper = mount(NodeInspectDialog, {
      props: { open: false, node: null },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    expect(wrapper.exists()).toBe(true)
  })

  it('fires the GET on open and shows the loading state', async () => {
    // Use a never-resolving promise so the loading
    // state remains visible at assertion time.
    vi.mocked(getStoredNodeKey).mockImplementation(
      () => new Promise(() => {}),
    )
    const wrapper = mount(NodeInspectDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    expect(getStoredNodeKey).toHaveBeenCalled()
    expect(wrapper.text()).toContain('nodes.inspectLoading')
  })

  it('shows the success card with public key + fingerprint when the row has a stored key', async () => {
    vi.mocked(getStoredNodeKey).mockResolvedValue(storedKey)
    const wrapper = mount(NodeInspectDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await vi.waitFor(
      () => {
        expect(wrapper.findAll('textarea').length).toBeGreaterThan(0)
      },
      { timeout: 1000 },
    )
    const inputs = wrapper.findAll('input')
    const fingerprintInput = inputs.find(
      (i) => i.element.value === storedKey.fingerprint,
    )
    expect(fingerprintInput).toBeDefined()
  })

  it('shows the empty state when the row has no stored key', async () => {
    vi.mocked(getStoredNodeKey).mockResolvedValue(emptyStoredKey)
    const wrapper = mount(NodeInspectDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await vi.waitFor(
      () => {
        expect(wrapper.text()).toContain('nodes.inspectNoKey')
      },
      { timeout: 1000 },
    )
    expect(wrapper.text()).toContain('nodes.inspectNoKeyHint')
  })

  it('emits failed on GET reject and shows the error state', async () => {
    vi.mocked(getStoredNodeKey).mockRejectedValue(new Error('boom'))
    const wrapper = mount(NodeInspectDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await vi.waitFor(
      () => {
        expect(wrapper.emitted('failed')).toBeDefined()
      },
      { timeout: 1000 },
    )
    const failedArgs = wrapper.emitted('failed')?.[0]
    expect(failedArgs?.[0]).toEqual(testNode)
    expect(failedArgs?.[1]).toContain('boom')
  })

  it('does not call getStoredNodeKey when node is null', async () => {
    mount(NodeInspectDialog, {
      props: { open: true, node: null },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await flushPromises()
    expect(getStoredNodeKey).not.toHaveBeenCalled()
  })
})
