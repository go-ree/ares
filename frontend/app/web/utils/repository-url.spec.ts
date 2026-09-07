import { describe, expect, it } from 'vitest';
import { repositoryWebURL } from './repository-url';

describe('repositoryWebURL', () => {
  it.each([
    ['https://github.com/go-ree/ares.git', 'https://github.com/go-ree/ares'],
    ['https://git.example.com:8443/team/service', 'https://git.example.com:8443/team/service'],
    ['git@github.com:go-ree/ares.git', 'https://github.com/go-ree/ares'],
    ['ssh://git@git.example.com/team/service.git', 'https://git.example.com/team/service'],
  ])('normalizes %s to a safe HTTPS link', (input, expected) => {
    expect(repositoryWebURL(input)).toBe(expected);
  });

  it.each([
    'http://git.example.com/team/service',
    'javascript:alert(1)',
    'data:text/html,unsafe',
    '//evil.example/team/service',
    'https://user:secret@git.example.com/team/service',
    'https://git.example.com/team/service?token=secret',
    'https://git.example.com/team/service#fragment',
    'https://git.example.com/../service',
    'https://git.example.com/%252e%252e/service',
    'https://git.example.com/%25252525252e%25252525252e/service',
    'https://git.example.com/%252f%252fevil.example/service',
    'https://git.example.com/team/%253fsecret',
    'https://git.example.com\\@evil.example/team/service',
    'git@evil.example:../service.git',
    'git@evil..example:team/service.git',
    'ssh://root@git.example.com/team/service.git',
    'ssh://git@git.example.com:2222/team/service.git',
  ])('rejects unsafe or ambiguous value %s', input => {
    expect(repositoryWebURL(input)).toBeNull();
  });
});
