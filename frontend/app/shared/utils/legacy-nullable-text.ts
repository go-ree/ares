/**
 * Normalize text fields that historically used the literal string "NULL" as
 * an empty-value sentinel. Keep this helper scoped to known domain fields so
 * legitimate user content is not rewritten globally.
 */
export const normalizeLegacyNullableText = (value: unknown): string => {
  if (typeof value !== 'string') return '';

  const normalized = value.trim();
  if (normalized === '' || normalized.toLowerCase() === 'null') return '';

  return normalized;
};
