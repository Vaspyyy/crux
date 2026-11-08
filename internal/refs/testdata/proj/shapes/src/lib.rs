pub mod shape;

pub fn add(a: i32, b: i32) -> i32 {
    a + b
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn adds_wrong() {
        assert_eq!(add(2, 2), 5);
    }

    #[test]
    fn returns_err() -> Result<(), String> {
        Err("no shape".to_string())
    }
}
