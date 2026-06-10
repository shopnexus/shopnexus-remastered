# TODO List

- confuse payment gateway with multi currency, sepay chỉ hỗ trợ currency VNĐ -> nếu buyer currency khác thì phải bị filter
- sửa lại restate error -> luôn dùng terminal error, sửa luôn http code ([500] [500] (500) get confirm session: no rows in result set\nRelated command: run [])
- smell on db.NullXX -> nó render cả {"Valid": true, "String": "abc"} -> nên custom lại để nó chỉ render ra String hoặc Int thôi

- nên bỏ sqlc luôn, generate repo code dựa trên DDL & struct tab db tren domain model để giảm 
- thêm 1 weighted criteria cho search:  popularity, sửa lại 