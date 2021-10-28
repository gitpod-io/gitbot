# deployed-labeler

> A go web server that labels Pull Requests with the `deployed: <team>` label

Accepts `POST` requests, while requiring to parameters:

* `commit`: Commit that has just been deployed to production.
* `team`: Which team just deployed to production.


`deployed-labeler` will look for the last 100 commits of the repository's default branch, alongside their associated Pull Requests and labels.

![image](https://user-images.githubusercontent.com/24193764/139254510-9f8ed8e1-e9ac-4177-b447-49932b804edd.png)


After that, it will add the `deployed: <team>` label only to the PRs where the `team` parameter matches with the already existing `team: <team>` label.

![image](https://user-images.githubusercontent.com/24193764/139254958-b8c08aee-3a51-477f-ac3c-8aad13bcd495.png)

