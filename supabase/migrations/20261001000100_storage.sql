-- Private storage bucket for memory files.
-- Layout: users/{user_id}/memories/{memory_id}/{original|thumbnail}

insert into storage.buckets (id, name, public, file_size_limit)
values ('memories', 'memories', false, 104857600) -- 100 MiB hard cap; plan limits are enforced by the API
on conflict (id) do update set public = false;

-- Users may only touch objects inside users/{their uid}/...
create policy "memories_objects_select_own" on storage.objects
  for select to authenticated
  using (
    bucket_id = 'memories'
    and (storage.foldername(name))[1] = 'users'
    and (storage.foldername(name))[2] = (select auth.uid())::text
  );

-- No INSERT/UPDATE policies: uploads only go through one-time signed upload
-- URLs issued by the backend after quota checks.

create policy "memories_objects_delete_own" on storage.objects
  for delete to authenticated
  using (
    bucket_id = 'memories'
    and (storage.foldername(name))[1] = 'users'
    and (storage.foldername(name))[2] = (select auth.uid())::text
  );
