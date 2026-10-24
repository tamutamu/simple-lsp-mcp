import os,sys,json,ast,traceback
from pathlib import Path
os.environ.update(SECRET_KEY="localbenchrandomsecret123",PROJECT_NAME="Benchmark",DATABASE_URL="postgresql://bench:benchlocal@127.0.0.1:5432/bench",FIRST_SUPERUSER="admin@example.com",FIRST_SUPERUSER_PASSWORD="BenchOnlyRandomPassword123")
root=Path(sys.argv[1]);case=sys.argv[2];sys.path.insert(0,str(root/"backend"))
from fastapi.testclient import TestClient
from sqlalchemy.pool import StaticPool
from sqlmodel import SQLModel,Session,create_engine
(root/"backend/app/frontend").mkdir(parents=True,exist_ok=True)
from app.main import app
from app.api.deps import get_db,get_current_user
from app.models import User,Item
from datetime import datetime,timezone
checks=[]
def ok(cond,msg):
 checks.append((msg,bool(cond)))
 if not cond:raise AssertionError(msg)
def test():
 pg_url=os.getenv("BENCH_POSTGRES_URL")
 if pg_url:
  from sqlalchemy.engine.url import make_url
  url=make_url(pg_url)
  if url.host not in ("127.0.0.1", "localhost") or url.database != "bench" or os.getenv("BENCH_ALLOW_DISPOSABLE_RESET") != "YES":
   raise RuntimeError("For safety, PostgreSQL grading requires a disposable localhost database named bench and BENCH_ALLOW_DISPOSABLE_RESET=YES")
 eng=create_engine(pg_url or "sqlite://",**({"connect_args":{"check_same_thread":False},"poolclass":StaticPool} if not pg_url else {}))
 if pg_url: SQLModel.metadata.drop_all(eng)
 SQLModel.metadata.create_all(eng)
 with Session(eng, expire_on_commit=False) as db:
  users=[User(email=f"u{i}@example.com",hashed_password="x",is_superuser=i==2) for i in range(3)]
  db.add_all(users);db.commit()
  for u in users:db.refresh(u)
  vals=[("Alpha Report","Budget draft",0),("Beta notes","ALPHA details",0),("Gamma",None,0),("alpha outside","Secret",1),("Delta","  ",1)]
  items=[Item(title=t,description=d,owner_id=users[o].id,created_at=datetime(2026,1,1,tzinfo=timezone.utc)) for t,d,o in vals]
  db.add_all(items);db.commit()
  for i in items:db.refresh(i)
  ids=[str(i.id) for i in items]
 current=[users[0]]
 def db_dep():
  with Session(eng) as session:yield session
 app.dependency_overrides[get_db]=db_dep
 app.dependency_overrides[get_current_user]=lambda:current[0]
 try:
  with TestClient(app) as c:
   prefix="/api/v1"
   get=lambda path:c.get(prefix+path)
   if case=="search":
    r=get("/items/?search=aLpHa");ok(r.status_code==200,"query success")
    j=r.json();ok(j["count"]==2,"scoped matched count")
    ok({x["id"] for x in j["data"]}==set(ids[:2]),"case insensitive title and description")
    ok(get("/items/?search=alpha&limit=1").json()["count"]==2,"count before paging")
    ok(len(get("/items/?search=alpha&limit=1").json()["data"])==1,"paging")
    ok(get("/items/?search=%20%20").json()["count"]==3,"empty search ignored")
    current[0]=users[1];j=get("/items/?search=alpha").json()
    ok(j["count"]==1 and j["data"][0]["id"]==ids[3],"tenant isolation")
    current[0]=users[2];ok(get("/items/?search=alpha").json()["count"]==3,"admin sees all")
   elif case=="paging":
    current[0]=users[2]  # user listing requires superuser authentication
    for path in ("/items/","/users/"):
     for arg in ("skip=-1","limit=0","limit=101"):
      ok(get(path+"?"+arg).status_code==422,path+" rejects "+arg)
    current[0]=users[2];full=get("/items/?limit=100").json()["data"]
    ok([x["id"] for x in full]==sorted(ids,reverse=True),"stable sort by created_at DESC then id DESC")
    ok(get("/items/?limit=2").json()["data"]==full[:2],"page 1")
    ok(get("/items/?skip=2&limit=2").json()["data"]==full[2:4],"page 2")
    ok(get("/users/?skip=0&limit=2").status_code==200,"users valid page")
   elif case=="access":
    tree=ast.parse((root/"backend/app/api/routes/items.py").read_text())
    fns={n.name:n for n in tree.body if isinstance(n,(ast.FunctionDef,ast.AsyncFunctionDef))}
    names=("read_item","update_item","delete_item")
    calls=[{n.func.id for n in ast.walk(fns[name]) if isinstance(n,ast.Call) and isinstance(n.func,ast.Name)} for name in names]
    ok(bool(set.intersection(*calls).intersection(fns.keys())),"shared private helper in all endpoints")
    ok(get(f"/items/{ids[0]}").status_code==200,"owner reads")
    ok(get(f"/items/{ids[3]}").status_code==403,"non-owner cannot read")
    ok(c.put(prefix+f"/items/{ids[3]}",json={"title":"Other"}).status_code==403,"non-owner cannot update")
    ok(c.delete(prefix+f"/items/{ids[3]}").status_code==403,"non-owner cannot delete")
    missing="00000000-0000-4000-8000-000000000001"
    ok(get("/items/"+missing).status_code==404,"missing read 404")
    ok(c.put(prefix+"/items/"+missing,json={"title":"Missing"}).status_code==404,"missing update 404")
    ok(c.delete(prefix+"/items/"+missing).status_code==404,"missing delete 404")
    current[0]=users[2]
    ok(get(f"/items/{ids[3]}").status_code==200,"admin read")
    ok(c.put(prefix+f"/items/{ids[3]}",json={"title":"AdminEdit"}).status_code==200,"admin update")
    ok(c.delete(prefix+f"/items/{ids[3]}").status_code==200,"admin delete")
   elif case=="summary":
    r=get("/items/summary");ok(r.status_code==200,"summary endpoint")
    ok(r.json()=={"total":3,"with_description":2,"without_description":1},"owner summary")
    current[0]=users[1];r=get("/items/summary")
    ok(r.status_code==200 and r.json()=={"total":2,"with_description":1,"without_description":1},"other owner summary")
    current[0]=users[2];r=get("/items/summary")
    ok(r.status_code==200 and r.json()=={"total":5,"with_description":3,"without_description":2},"admin summary")
    spec=c.get("/api/v1/openapi.json").json()
    ok("get" in spec["paths"].get("/api/v1/items/summary",{}),"openapi route")
    ok("ItemSummary" in str(spec),"named response schema")
   elif case in ("rename_user","rename_password"):
    import importlib
    old,new=("get_user_by_email","find_user_by_email") if case=="rename_user" else ("get_password_hash","hash_password")
    files=list((root/"backend/app").rglob("*.py"))+list((root/"backend/tests").rglob("*.py"))
    defs=[];uses=[];old_refs=[]
    for file in files:
     tree=ast.parse(file.read_text())
     for node in ast.walk(tree):
      if isinstance(node,(ast.FunctionDef,ast.AsyncFunctionDef)) and node.name in (old,new):
       defs.append((str(file.relative_to(root)),node.name))
      if isinstance(node,(ast.Attribute,ast.Name,ast.ImportFrom)):
       names=[node.attr] if isinstance(node,ast.Attribute) else ([node.id] if isinstance(node,ast.Name) else [n.name for n in node.names])
       if old in names:old_refs.append(str(file.relative_to(root)))
       if new in names:uses.append(str(file.relative_to(root)))
    ok(sum(n==new for _,n in defs)==1 and sum(n==old for _,n in defs)==0,"one new declaration and no old declaration")
    ok(not old_refs,"no stale name/import/attribute references")
    ok(len(set(uses))>=4 and len(uses)>=8,"all relevant call sites migrated")
    if case=="rename_user":
     from app import crud
     with Session(eng) as db:
      ok(crud.find_user_by_email(session=db,email=users[0].email).id==users[0].id,"crud lookup still works")
      ok(crud.find_user_by_email(session=db,email="nobody@example.com") is None,"absent user lookup")
    else:
     from app.core import security
     h=security.hash_password("BenchPassword124!")
     verified,_=security.verify_password("BenchPassword124!",h)
     ok(verified,"password hash and verify roundtrip")
   else:raise ValueError(case)
 finally:app.dependency_overrides.clear()
try:test()
except Exception as e:
 print(json.dumps({"case":case,"success":False,"checks":checks,"error":str(e),"traceback":traceback.format_exc(limit=2)}))
 sys.exit(1)
print(json.dumps({"case":case,"success":True,"checks":checks}))
