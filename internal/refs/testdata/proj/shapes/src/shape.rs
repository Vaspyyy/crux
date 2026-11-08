pub struct Circle {
    pub r: f64,
}

impl Circle {
    pub fn area(&self) -> f64 {
        if self.r < 0.0 {
            panic!("negative radius");
        }
        3.14 * self.r * self.r
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn negative() {
        Circle { r: -1.0 }.area();
    }
}
