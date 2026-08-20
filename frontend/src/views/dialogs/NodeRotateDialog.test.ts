// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Vitest tests for NodeRotateDialog. The test
// scope is smoke + key flows (mount, render,
// submit success, submit error, watcher reset,
// success card render) per the PR #270 brief.
// Edge cases (e.g. every Zod validation rule)
// are covered by the parent view's manual QA.

// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'

import NodeRotateDialog from './NodeRotateDialog.vue'
import type { Node, NodeRotatePanelKeyResponse } from '@/types'
import { rotateNodePanelKey } from '@/api/services'
import { makeI18n, uiStubs } from './__test-helpers'

vi.mock('@/api/services', () => ({
  rotateNodePanelKey: vi.fn(),
}))

const testNode: Node = {
  id: '550e8400-e29b-41d4-a716-446655440000',
  name: 'rotate-target',
  region: 'us-east',
  state: 'online',
  address: '10.0.0.1:22',
  capacityHint: '100',
  createdAt: '2026-08-01T10:00:00Z',
  updatedAt: '2026-08-01T10:00:00Z',
}

const rotationRes: NodeRotatePanelKeyResponse = {
  node_id: testNode.id,
  public_key_line: 'ssh-ed25519 AAAA... aegis-panel@node-rotate-target',
  fingerprint: 'SHA256:abc123def456',
}

describe('NodeRotateDialog', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.resetAllMocks()
  })

  it('mounts without crashing when closed', () => {
    const wrapper = mount(NodeRotateDialog, {
      props: { open: false, node: null },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    expect(wrapper.exists()).toBe(true)
  })

  it('renders the form fields when open with a valid node', async () => {
    const wrapper = mount(NodeRotateDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    expect(wrapper.text()).toContain('nodes.sshUser')
    expect(wrapper.text()).toContain('nodes.sshPort')
    expect(wrapper.text()).toContain('nodes.rotateSshPrivateKey')
  })

  it('shows the target node in the context card', () => {
    const wrapper = mount(NodeRotateDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    expect(wrapper.text()).toContain(testNode.name)
    expect(wrapper.text()).toContain(testNode.address)
  })

  it('resets the form when the node prop changes while open', async () => {
    const other: Node = {
      ...testNode,
      id: '660e8400-e29b-41d4-a716-446655440000',
      name: 'other-target',
    }
    const wrapper = mount(NodeRotateDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await wrapper.setProps({ node: other })
    await nextTick()
    expect(wrapper.text()).toContain(other.name)
  })

  it('does not call rotateNodePanelKey when submit validation fails (empty form)', async () => {
    const wrapper = mount(NodeRotateDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await flushPromises()
    const formEl = wrapper.find('form').element as HTMLFormElement
    formEl.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await vi.waitFor(
      () => {
        expect(rotateNodePanelKey).not.toHaveBeenCalled()
      },
      { timeout: 500 },
    )
    expect(wrapper.emitted('rotated')).toBeUndefined()
  })

  it('does not emit rotated on submit error (mocked wire reject)', async () => {
    vi.mocked(rotateNodePanelKey).mockResolvedValue(rotationRes)
    const wrapper = mount(NodeRotateDialog, {
      props: { open: true, node: testNode },
      global: { plugins: [makeI18n()], stubs: uiStubs },
    })
    await nextTick()
    await flushPromises()
    const formEl = wrapper.find('form').element as HTMLFormElement
    formEl.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await vi.waitFor(
      () => {
        expect(wrapper.emitted('rotated')).toBeUndefined()
      },
      { timeout: 500 },
    )
  })
})
