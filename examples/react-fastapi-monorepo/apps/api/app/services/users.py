from app.models import User


def normalize_user_name(name: str) -> str:
    return name.strip()


def get_users() -> list[User]:
    return [User(id=1, name=normalize_user_name(" Ada "))]
