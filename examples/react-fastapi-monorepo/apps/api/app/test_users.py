from app.routes.users import list_users


def test_list_users_smoke() -> None:
    assert list_users()[0].name == "Ada"
