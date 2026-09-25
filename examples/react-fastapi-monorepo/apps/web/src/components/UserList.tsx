import { fetchUsers } from "../services/users";

export async function loadUsers(apiBase: string) {
  return fetchUsers(apiBase);
}

export async function UserList() {
  const users = await loadUsers("http://localhost:8000");
  return (
    <ul>
      {users.map((user) => (
        <li key={user.id}>{user.name}</li>
      ))}
    </ul>
  );
}
