// SPDX-License-Identifier: AGPL-3.0-or-later
//
// useZodForm — typed wrapper around vee-validate's
// `useForm` with a zod schema as the source of
// truth for both the initial values (via
// `z.infer`) and the validation rules.
//
// The composable is the bridge between the zod
// schemas in `@/schemas` and the form components in
// `@/components/Form*.vue`. Every form in the app
// uses this composable; the components are
// presentation-only and never touch the schema
// directly.

import { toTypedSchema } from '@vee-validate/zod'
import { useForm } from 'vee-validate'
import type { z, ZodTypeAny } from 'zod'

export interface UseZodFormOptions<T extends ZodTypeAny> {
  /** zod schema for the form's payload. The
   * composable derives the TypeScript type from it.
   */
  schema: T
  /** Initial values. If omitted, zod's `.default`
   * values are used (zod returns them via
   * `schema.parse({})`).
   */
  initialValues?: Partial<z.infer<T>>
  /** Submit handler. Runs only after validation
   * passes.
   */
  onSubmit: (values: z.infer<T>) => void | Promise<void>
}

/**
 * Typed form handle. The shape is the same as
 * vee-validate's `useForm` return, with one
 * narrowing: `values` is typed as the schema's
 * inferred type and `handleSubmit` is pre-bound
 * to the caller-supplied `onSubmit` so the
 * template only needs `@submit="handleSubmit"`.
 */
export function useZodForm<TSchema extends ZodTypeAny>(
  options: UseZodFormOptions<TSchema>,
) {
  // `z.infer<T>` only works on a `const` schema, so
  // we re-derive it from the runtime instance. This
  // is a known limitation of `z.infer`; the workaround
  // is the explicit generic on `useZodForm` which
  // captures the schema at the call site.
  type Values = z.infer<TSchema>

  const { handleSubmit, ...rest } = useForm<Values>({
    validationSchema: toTypedSchema(options.schema),
    initialValues: options.initialValues as Values | undefined,
  })

  return {
    handleSubmit: handleSubmit(options.onSubmit),
    ...rest,
  }
}
