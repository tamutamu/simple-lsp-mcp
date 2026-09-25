import { loadUsers } from "./UserList";

export async function smokeTest() {
  return loadUsers("http://localhost:8000");
}
