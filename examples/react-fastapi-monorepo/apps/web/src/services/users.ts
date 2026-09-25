import type { User } from "../types/user";

export function normalizeUserName(name: string): string {
  return name.trim();
}

export async function fetchUsers(apiBase: string): Promise<User[]> {
  const response = await fetch(`${apiBase}/users`);
  const users = (await response.json()) as User[];
  return users.map((user) => ({ ...user, name: normalizeUserName(user.name) }));
}
