from fastapi import APIRouter

from app.models import User
from app.services.users import get_users

router = APIRouter()


@router.get("/users")
def list_users() -> list[User]:
    return get_users()
